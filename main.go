package main

import (
	"context"
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
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
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

type AnalyticsEvent struct {
	ShortCode string
	IPAddress string
	UserAgent string
	Timestamp time.Time
}

type URLShortener struct {
	db               *sql.DB
	analyticsChannel chan AnalyticsEvent
	redisClient      *redis.Client
	wg               sync.WaitGroup
}

func NewURLShortener(dbURL string, redisAddr string) (*URLShortener, error) {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(15)
	db.SetConnMaxLifetime(10 * time.Minute)

	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: "",
		DB:       0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = rdb.Ping(ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	us := &URLShortener{
		db:               db,
		analyticsChannel: make(chan AnalyticsEvent, 1000),
		redisClient:      rdb,
	}

	if err := us.createTables(); err != nil {
		return nil, err
	}

	go us.analyticsWorker()

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
		`CREATE INDEX IF NOT EXISTS idx_urls_long_url ON urls(long_url);`,
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

func (us *URLShortener) analyticsWorker() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	batch := make([]AnalyticsEvent, 0, 50)

	for {
		select {
		case event := <-us.analyticsChannel:
			batch = append(batch, event)
			if len(batch) >= 50 {
				us.processBatch(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				us.processBatch(batch)
				batch = batch[:0]
			}
		}
	}
}

func (us *URLShortener) processBatch(events []AnalyticsEvent) {
	if len(events) == 0 {
		return
	}

	tx, err := us.db.Begin()
	if err != nil {
		log.Printf("Error starting analytics transaction: %v", err)
		return
	}
	defer tx.Rollback()

	updateStmt, err := tx.Prepare("UPDATE urls SET clicks = clicks + 1 WHERE short_code = $1")
	if err != nil {
		log.Printf("Error preparing update statement: %v", err)
		return
	}
	defer updateStmt.Close()

	insertStmt, err := tx.Prepare("INSERT INTO analytics (short_code, ip_address, user_agent, timestamp) VALUES ($1, $2, $3, $4)")
	if err != nil {
		log.Printf("Error preparing insert statement: %v", err)
		return
	}
	defer insertStmt.Close()

	for _, event := range events {
		if _, err := updateStmt.Exec(event.ShortCode); err != nil {
			log.Printf("Error updating clicks for %s: %v", event.ShortCode, err)
			continue
		}

		if _, err := insertStmt.Exec(event.ShortCode, event.IPAddress, event.UserAgent, event.Timestamp); err != nil {
			log.Printf("Error inserting analytics for %s: %v", event.ShortCode, err)
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("Error committing analytics batch: %v", err)
	}
}

func generateShortCode(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)

	for i := range result {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		result[i] = charset[num.Int64()]
	}
	return string(result), nil
}

func (us *URLShortener) generateUniqueShortCode(ctx context.Context) (string, error) {
	maxAttempts := 10
	startLength := 6

	for length := startLength; length <= startLength+2; length++ {
		for attempt := 0; attempt < maxAttempts; attempt++ {
			shortCode, err := generateShortCode(length)
			if err != nil {
				return "", err
			}

			var count int
			err = us.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", shortCode).Scan(&count)
			if err != nil {
				return "", err
			}
			if count == 0 {
				return shortCode, nil
			}
		}
	}
	return "", fmt.Errorf("failed to generate unique short code after multiple attempts")
}

func isValidURL(str string) bool {
	u, err := url.Parse(str)
	return err == nil && u.Scheme != "" && u.Host != ""
}

func (us *URLShortener) ShortenURL(ctx context.Context, longURL string) (*URL, error) {
	if !isValidURL(longURL) {
		return nil, fmt.Errorf("invalid URL format")
	}

	var existingURL URL
	err := us.db.QueryRowContext(ctx,
		"SELECT id, short_code, long_url, clicks, created_at FROM urls WHERE long_url = $1",
		longURL).Scan(&existingURL.ID, &existingURL.ShortCode, &existingURL.LongURL, &existingURL.Clicks, &existingURL.CreatedAt)

	if err == nil {
		urlJSON, _ := json.Marshal(existingURL)
		us.redisClient.Set(ctx, existingURL.ShortCode, urlJSON, 24*time.Hour)
		return &existingURL, nil

	}

	shortCode, err := us.generateUniqueShortCode(ctx)
	if err != nil {
		return nil, err
	}

	var newURL URL
	err = us.db.QueryRowContext(ctx,
		"INSERT INTO urls (short_code, long_url) VALUES ($1, $2) RETURNING id, short_code, long_url, clicks, created_at",
		shortCode, longURL,
	).Scan(&newURL.ID, &newURL.ShortCode, &newURL.LongURL, &newURL.Clicks, &newURL.CreatedAt)

	if err != nil {
		return nil, err
	}

	newURLJSON, _ := json.Marshal(newURL)
	us.redisClient.Set(ctx, newURL.ShortCode, newURLJSON, 24*time.Hour)
	return &newURL, nil
}

func (us *URLShortener) GetURL(ctx context.Context, shortCode string) (*URL, error) {
	cachedURLJSON, err := us.redisClient.Get(ctx, shortCode).Result()
	if err == nil {
		var urlRecord URL
		jsonErr := json.Unmarshal([]byte(cachedURLJSON), &urlRecord)
		if jsonErr == nil {
			return &urlRecord, nil
		}
		log.Printf("Error unmarshaling cached URL for %s: %v", shortCode, jsonErr)
	} else if err != redis.Nil {
		log.Printf("Error getting from Redis for %s: %v", shortCode, err)
	}

	var urlRecord URL
	err = us.db.QueryRowContext(ctx,
		"SELECT id, short_code, long_url, clicks, created_at FROM urls WHERE short_code = $1",
		shortCode).Scan(&urlRecord.ID, &urlRecord.ShortCode, &urlRecord.LongURL, &urlRecord.Clicks, &urlRecord.CreatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("short URL not found")
		}
		return nil, err
	}

	urlJSON, _ := json.Marshal(urlRecord)
	us.redisClient.Set(ctx, shortCode, urlJSON, 24*time.Hour)
	return &urlRecord, nil
}

func (us *URLShortener) RecordAnalytics(shortCode, ipAddress, userAgent string) {
	event := AnalyticsEvent{
		ShortCode: shortCode,
		IPAddress: ipAddress,
		UserAgent: userAgent,
		Timestamp: time.Now(),
	}

	select {
	case us.analyticsChannel <- event:
		//successful enqueueing
	default:
		//channel is full, drop (later add some fallback here)
		log.Printf("Analytics channel full, dropping event for %s", shortCode)
	}

}

func (us *URLShortener) GetAnalytics(ctx context.Context, shortCode string) ([]AnalyticsRecord, error) {
	rows, err := us.db.QueryContext(ctx,
		"SELECT id, short_code, ip_address, user_agent, timestamp FROM analytics WHERE short_code = $1 ORDER BY timestamp DESC LIMIT 1000",
		shortCode)

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

	return analytics, rows.Err()
}

//HTTP handlers

func (us *URLShortener) shortenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

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

	urlRecord, err := us.ShortenURL(ctx, request.URL)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			http.Error(w, "Request timeout", http.StatusRequestTimeout)
			return
		}
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
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	vars := mux.Vars(r)
	shortCode := vars["shortCode"]

	if shortCode == "" {
		http.Error(w, "Short code is required", http.StatusBadRequest)
		return
	}

	urlRecord, err := us.GetURL(ctx, shortCode)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			http.Error(w, "Request timeout", http.StatusRequestTimeout)
			return
		}
		http.Error(w, "Short URL not found", http.StatusNotFound)
		return
	}

	ipAddress := r.RemoteAddr
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		ipAddress = strings.Split(forwarded, ",")[0]
	}
	userAgent := r.UserAgent()

	us.RecordAnalytics(shortCode, ipAddress, userAgent)

	http.Redirect(w, r, urlRecord.LongURL, http.StatusMovedPermanently)
}

func (us *URLShortener) statsHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	vars := mux.Vars(r)
	shortCode := vars["shortCode"]

	if shortCode == "" {
		http.Error(w, "Short code is required", http.StatusBadRequest)
		return
	}

	urlRecord, err := us.GetURL(ctx, shortCode)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			http.Error(w, "Request timeout", http.StatusRequestTimeout)
			return
		}
		http.Error(w, "Short URL not found", http.StatusNotFound)
		return
	}

	err = us.db.QueryRowContext(ctx, "SELECT clicks FROM urls WHERE short_code = $1", shortCode).Scan(&urlRecord.Clicks)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			http.Error(w, "Request timeout", http.StatusRequestTimeout)
			return
		}
		http.Error(w, "Error retrieving stats", http.StatusInternalServerError)
		return
	}

	analytics, err := us.GetAnalytics(ctx, shortCode)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			http.Error(w, "Request timeout", http.StatusRequestTimeout)
			return
		}
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

func (us *URLShortener) Close() error {
	close(us.analyticsChannel)
	us.wg.Wait()

	if us.redisClient != nil {
		if err := us.redisClient.Close(); err != nil {
			log.Printf("Error closing Redis client: %v", err)
		}
	}

	return us.db.Close()
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	redisAddr := os.Getenv("REDIS_ADDR") // Get Redis address
	if redisAddr == "" {
		log.Fatal("REDIS_ADDR environment variable is required")
	}

	shortener, err := NewURLShortener(dbURL, redisAddr)
	if err != nil {
		log.Fatal("Failed to initialize URL shortener:", err)
	}
	defer shortener.Close()

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

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	fmt.Println("URL Shortener started on http://localhost:8080")
	fmt.Println("Visit http://localhost:8080 for the web interface")
	log.Fatal(server.ListenAndServe())
}
