package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
)

type URL struct {
	ID        int       `json:"id"`
	ShortCode string    `json:"short_code"`
	LongURL   string    `json:"long_url"`
	Clicks    int       `json:"clicks"`
	CreatedAt time.Time `json:"created_at"`
}

type AnalyticsRecord struct {
	ID        int       `json:"id"`
	ShortCode string    `json:"short_code"`
	IPAddress string    `json:"ip_address"`
	UserAgent string    `json:"user_agent"`
	Timestamp time.Time `json:"timestamp"`
}

type URLShortener struct {
	db *sql.DB
}

func NewURLShortener(dbURL string) (*URLShortener, error) {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	us := &URLShortener{db: db}
	if err := us.createTables(); err != nil {
		return nil, err
	}
	return us, nil
}

func (us *URLShortener) createTables() error {

	urlsTable := `
	CREATE TABLE IF NOT EXISTS urls (
		id SERIAL PRIMARY KEY,
		short_code TEXT UNIQUE NOT NULL,
		long_url TEXT NOT NULL,
		clicks INTEGER DEFAULT 0,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	analyticsTable := `
	CREATE TABLE IF NOT EXISTS analytics (
		id SERIAL PRIMARY KEY,
		short_code TEXT NOT NULL,
		ip_address TEXT,
		user_agent TEXT,
		timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (short_code) REFERENCES urls(short_code)
	);`

	indexQueries := []string{
		`CREATE INDEX IF NOT EXISTS idx_urls_short_code ON urls(short_code);`,
		`CREATE INDEX IF NOT EXISTS idx_urls_created_at ON urls(created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_analytics_short_code ON analytics(short_code);`,
		`CREATE INDEX IF NOT EXISTS idx_analytics_timestamp ON analytics(timestamp DESC);`,
	}

	if _, err := us.db.Exec(urlsTable); err != nil {
		return err
	}

	if _, err := us.db.Exec(analyticsTable); err != nil {
		return err
	}

	for _, query := range indexQueries {
		if _, err := us.db.Exec(query); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

func generateShortCode(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)

	for i := range result {
		num, err := rand.Int(rand.Reader, big.NewInt((int64(len(charset)))))
		if err != nil {
			return "", err
		}
		result[i] = charset[num.Int64()]
	}
	return string(result), nil
}

func isValidURL(str string) bool {
	u, err := url.Parse(str)
	return err == nil && u.Scheme != "" && u.Host != ""
}

func (us *URLShortener) ShortenURL(longURL string) (*URL, error) {
	if !isValidURL(longURL) {
		return nil, fmt.Errorf("invalid URL format")
	}

	var existingURL URL
	err := us.db.QueryRow("SELECT id, short_code, long_url, clicks, created_at FROM urls WHERE long_url = $1", longURL).
		Scan(&existingURL.ID, &existingURL.ShortCode, &existingURL.LongURL, &existingURL.Clicks, &existingURL.CreatedAt)

	if err == nil {
		return &existingURL, nil
	}

	//the below solution might not be the most efficient one?
	var shortCode string
	for {
		shortCode, err = generateShortCode(6)
		if err != nil {
			return nil, err
		}

		var count int
		err = us.db.QueryRow("SELECT COUNT(*) FROM urls WHERE short_code = $1", shortCode).Scan(&count)
		if err != nil {
			return nil, err
		}
		if count == 0 {
			break
		}
	}

	result, err := us.db.Exec("INSERT INTO urls (short_code, long_url) VALUES ($1, $2)", shortCode, longURL)
	if err != nil {
		return nil, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	return &URL{
		ID:        int(id),
		ShortCode: shortCode,
		LongURL:   longURL,
		Clicks:    0,
		CreatedAt: time.Now(),
	}, nil
}

func (us *URLShortener) GetURL(shortCode string) (*URL, error) {
	var urlRecord URL
	err := us.db.QueryRow("SELECT id, short_code, long_url, clicks, created_at FROM urls WHERE short_code = $1", shortCode).
		Scan(&urlRecord.ID, &urlRecord.ShortCode, &urlRecord.LongURL, &urlRecord.Clicks, &urlRecord.CreatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("short URL not found")
		}
		return nil, err
	}

	return &urlRecord, nil
}

func (us *URLShortener) IncrementClick(shortCode, ipAddress, userAgent string) error {
	_, err := us.db.Exec("UPDATE urls SET clicks = clicks + 1 WHERE short_code = $1", shortCode)

	if err != nil {
		return err
	}

	_, err = us.db.Exec("INSERT INTO analytics (short_code, ip_address, user_agent) VALUES ($1, $2, $3)",
		shortCode, ipAddress, userAgent)

	return err
}

func (us *URLShortener) GetAnalytics(shortCode string) ([]AnalyticsRecord, error) {
	rows, err := us.db.Query("SELECT id, short_code, ip_address, user_agent, timestamp FROM analytics WHERE short_code = $1 ORDER BY timestamp DESC", shortCode)

	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var analytics []AnalyticsRecord
	for rows.Next() {
		var record AnalyticsRecord
		err := rows.Scan(&record.ID, &record.ShortCode, &record.IPAddress, &record.UserAgent, &record.Timestamp)
		if err != nil {
			return nil, err
		}
		analytics = append(analytics, record)
	}

	return analytics, nil
}

//HTTP handlers

func (us *URLShortener) shortenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		URL string `json:"url"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if request.URL == "" {
		http.Error(w, "URL is required", http.StatusBadRequest)
		return
	}

	urlRecord, err := us.ShortenURL(request.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"short_url":  fmt.Sprintf("http://localhost:8080/%s", urlRecord.ShortCode),
		"short_code": urlRecord.ShortCode,
		"long_url":   urlRecord.LongURL,
		"created_at": urlRecord.CreatedAt,
	})
}

func (us *URLShortener) redirectHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	shortCode := vars["shortCode"]

	if shortCode == "" {
		http.Error(w, "Short code is required", http.StatusBadRequest)
		return
	}

	urlRecord, err := us.GetURL(shortCode)
	if err != nil {
		http.Error(w, "Short URL not found", http.StatusNotFound)
		return
	}

	ipAddress := r.RemoteAddr
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		ipAddress = strings.Split(forwarded, ",")[0]
	}
	userAgent := r.UserAgent()

	if err := us.IncrementClick(shortCode, ipAddress, userAgent); err != nil {
		log.Printf("Error recording analytics: %v", err)
	}

	http.Redirect(w, r, urlRecord.LongURL, http.StatusMovedPermanently)
}

func (us *URLShortener) statsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	shortCode := vars["shortCode"]

	if shortCode == "" {
		http.Error(w, "Short code is required", http.StatusBadRequest)
		return
	}

	urlRecord, err := us.GetURL(shortCode)
	if err != nil {
		http.Error(w, "Short URL not found", http.StatusNotFound)
		return
	}

	err = us.db.QueryRow("SELECT clicks FROM urls WHERE short_code = $1", shortCode).Scan(&urlRecord.Clicks)
	if err != nil {
		http.Error(w, "Error retrieving stats", http.StatusInternalServerError)
		return
	}

	analytics, err := us.GetAnalytics(shortCode)
	if err != nil {
		http.Error(w, "Error retrieving analytics", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"short_code": urlRecord.ShortCode,
		"long_url":   urlRecord.LongURL,
		"clicks":     urlRecord.Clicks,
		"created_at": urlRecord.CreatedAt,
		"analytics":  analytics,
	})
}

func (us *URLShortener) listHandler(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	rows, err := us.db.Query("SELECT id, short_code, long_url, clicks, created_at FROM urls ORDER BY created_at DESC LIMIT $1", limit)
	if err != nil {
		http.Error(w, "Error retrieving URLs", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var urls []URL
	for rows.Next() {
		var url URL
		err := rows.Scan(&url.ID, &url.ShortCode, &url.LongURL, &url.Clicks, &url.CreatedAt)
		if err != nil {
			http.Error(w, "Error scanning URL", http.StatusInternalServerError)
			return
		}
		urls = append(urls, url)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"urls":  urls,
		"count": len(urls),
	})
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	html, err := os.ReadFile("templates/home.html")
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		log.Println("Error reading HTML file:", err)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	shortener, err := NewURLShortener(dbURL)

	if err != nil {
		log.Fatal("Failed to initialize URL shortener:", err)
	}
	defer shortener.db.Close()

	r := mux.NewRouter()

	r.HandleFunc("/health", healthHandler).Methods("GET")

	r.HandleFunc("/", homeHandler).Methods("GET")

	r.HandleFunc("/api/shorten", shortener.shortenHandler).Methods("POST")
	r.HandleFunc("/api/stats/{shortCode}", shortener.statsHandler).Methods("GET")
	r.HandleFunc("/api/list", shortener.listHandler).Methods("GET")
	r.HandleFunc("/{shortCode}", shortener.redirectHandler).Methods("GET")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Println("URL Shortener started on http://localhost:8080")
	fmt.Println("Visit http://localhost:8080 for the web interface")
	log.Fatal(http.ListenAndServe(":8080", r))
}
