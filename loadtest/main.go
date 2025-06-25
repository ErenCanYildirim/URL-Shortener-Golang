package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	BaseURL     = "http://localhost:8080"
	Duration    = 30 * time.Second
	Workers     = 10
	RequestRate = 50
)

type TestResult struct {
	Name           string
	TotalRequests  int
	SuccessCount   int
	ErrorCount     int
	MinLatency     time.Duration
	MaxLatency     time.Duration
	AvgLatency     time.Duration
	RequestsPerSec float64
	Errors         []string
}

type LoadTester struct {
	client *http.Client
}

func NewLoadTester() *LoadTester {
	return &LoadTester{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (lt *LoadTester) makeRequest(method, url string, body []byte, headers map[string]string) (*http.Response, time.Duration, error) {
	start := time.Now()

	var req *http.Request
	var err error

	if body != nil {
		req, err = http.NewRequest(method, url, bytes.NewBuffer(body))
	} else {
		req, err = http.NewRequest(method, url, nil)
	}

	if err != nil {
		return nil, 0, err
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := lt.client.Do(req)
	latency := time.Since(start)

	return resp, latency, err
}

func (lt *LoadTester) runTest(name string, testFunc func() (bool, time.Duration, error)) TestResult {
	fmt.Printf("Running %s load test...\n", name)

	var wg sync.WaitGroup
	var mu sync.Mutex

	result := TestResult{
		Name:       name,
		MinLatency: time.Hour,
		Errors:     make([]string, 0),
	}

	startTime := time.Now()
	endTime := startTime.Add(Duration)

	delay := time.Duration(int64(time.Second) / int64(RequestRate/Workers))

	for i := 0; i < Workers; i++ {
		wg.Add(1)
		go func(workerId int) {
			defer wg.Done()

			ticker := time.NewTicker(delay)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					if time.Now().After(endTime) {
						return
					}

					success, latency, err := testFunc()

					mu.Lock()
					result.TotalRequests++

					if err != nil {
						result.ErrorCount++
						if len(result.Errors) < 10 {
							result.Errors = append(result.Errors, err.Error())
						}
					} else if success {
						result.SuccessCount++
					} else {
						result.ErrorCount++
					}

					if latency < result.MinLatency {
						result.MinLatency = latency
					}
					if latency > result.MaxLatency {
						result.MaxLatency = latency
					}
					mu.Unlock()
				}
			}
		}(i)
	}

	wg.Wait()

	if result.MinLatency == time.Hour {
		result.MinLatency = 0
	}

	totalDuration := time.Since(startTime)
	result.RequestsPerSec = float64(result.TotalRequests) / totalDuration.Seconds()

	if result.SuccessCount > 0 {
		result.AvgLatency = time.Duration(int64(result.MaxLatency+result.MinLatency) / 2)
	}

	return result
}

func (lt *LoadTester) testHealthCheck() (bool, time.Duration, error) {
	resp, latency, err := lt.makeRequest("GET", BaseURL+"/health", nil, nil)
	if err != nil {
		return false, latency, err
	}
	defer resp.Body.Close()

	return resp.StatusCode == 200, latency, nil
}

func (lt *LoadTester) testHomePage() (bool, time.Duration, error) {
	resp, latency, err := lt.makeRequest("GET", BaseURL+"/", nil, nil)
	if err != nil {
		return false, latency, err
	}
	defer resp.Body.Close()

	return resp.StatusCode == 200, latency, nil
}

func (lt *LoadTester) testShortenURL() (bool, time.Duration, error) {
	urls := []string{
		"https://www.google.com",
		"https://www.github.com",
		"https://www.stackoverflow.com",
		"https://www.reddit.com",
		"https://www.youtube.com",
		"https://www.twitter.com",
		"https://www.facebook.com",
		"https://www.linkedin.com",
		"https://www.amazon.com",
		"https://www.netflix.com",
	}

	//Random modification of URLs to make them unique
	selectedURL := urls[rand.Intn(len(urls))] + "?test=" + fmt.Sprintf("%d", rand.Intn(10000))

	payload := map[string]string{
		"url": selectedURL,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return false, 0, err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	resp, latency, err := lt.makeRequest("POST", BaseURL+"/api/shorten", jsonPayload, headers)
	if err != nil {
		return false, latency, err
	}
	defer resp.Body.Close()

	return resp.StatusCode == 200, latency, nil
}

func (lt *LoadTester) testListURLs() (bool, time.Duration, error) {
	resp, latency, err := lt.makeRequest("GET", BaseURL+"/api/list?limit=20", nil, nil)
	if err != nil {
		return false, latency, err
	}
	defer resp.Body.Close()

	return resp.StatusCode == 200, latency, nil
}

var shortCodes []string
var shortCodesMu sync.Mutex

func (lt *LoadTester) collectShortCodes() {
	fmt.Println("Collecting short codes for redirect tests...")

	urls := []string{
		"https://www.google.com",
		"https://www.github.com",
		"https://www.stackoverflow.com",
		"https://www.reddit.com",
		"https://www.youtube.com",
		"https://www.twitter.com",
		"https://www.facebook.com",
		"https://www.linkedin.com",
		"https://www.amazon.com",
		"https://www.netflix.com",
	}

	for _, url := range urls {
		payload := map[string]string{"url": url}
		jsonPayload, _ := json.Marshal(payload)

		headers := map[string]string{"Content-Type": "application/json"}
		resp, _, err := lt.makeRequest("POST", BaseURL+"/api/shorten", jsonPayload, headers)

		if err == nil && resp.StatusCode == 200 {
			var result map[string]interface{}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if json.Unmarshal(body, &result) == nil {
				if shortCode, ok := result["short_code"].(string); ok {
					shortCodes = append(shortCodes, shortCode)
				}
			}
		}
	}

	fmt.Printf("Collected %d short codes\n", len(shortCodes))
}

func (lt *LoadTester) testRedirect() (bool, time.Duration, error) {
	shortCodesMu.Lock()
	if len(shortCodes) == 0 {
		shortCodesMu.Unlock()
		return false, 0, fmt.Errorf("no short codes available")
	}

	shortCode := shortCodes[rand.Intn(len(shortCodes))]
	shortCodesMu.Unlock()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 10 * time.Second,
	}

	start := time.Now()
	resp, err := client.Get(BaseURL + "/" + shortCode)
	latency := time.Since(start)

	if err != nil {
		return false, latency, err
	}
	defer resp.Body.Close()

	return resp.StatusCode == 301, latency, nil
}

func (lt *LoadTester) testStats() (bool, time.Duration, error) {
	shortCodesMu.Lock()
	if len(shortCodes) == 0 {
		shortCodesMu.Unlock()
		return false, 0, fmt.Errorf("no short codes available")
	}

	shortCode := shortCodes[rand.Intn(len(shortCodes))]
	shortCodesMu.Unlock()

	resp, latency, err := lt.makeRequest("GET", BaseURL+"/api/stats/"+shortCode, nil, nil)
	if err != nil {
		return false, latency, err
	}
	defer resp.Body.Close()

	return resp.StatusCode == 200, latency, nil
}

func printResults(results []TestResult) {
	fmt.Println("\n")
	fmt.Println("LOAD TEST RESULTS SUMMARY")
	fmt.Println("\n")

	totalRequests := 0
	totalSuccess := 0
	totalErrors := 0

	for _, result := range results {
		fmt.Printf("\n%s:\n", result.Name)
		fmt.Printf("   Total Requests: %d\n", result.TotalRequests)
		fmt.Printf("   Successful: %d (%.1f%%)\n", result.SuccessCount,
			float64(result.SuccessCount)/float64(result.TotalRequests)*100)
		fmt.Printf("   Errors: %d (%.1f%%)\n", result.ErrorCount,
			float64(result.ErrorCount)/float64(result.TotalRequests)*100)
		fmt.Printf("   Requests/sec: %.2f\n", result.RequestsPerSec)
		fmt.Printf("   Min Latency: %v\n", result.MinLatency)
		fmt.Printf("   Max Latency: %v\n", result.MaxLatency)
		fmt.Printf("   Avg Latency: %v\n", result.AvgLatency)

		if len(result.Errors) > 0 {
			fmt.Printf("   Sample Errors:\n")
			for i, err := range result.Errors {
				if i >= 3 {
					break
				}
				fmt.Printf("     - %s\n", err)
			}
		}

		totalRequests += result.TotalRequests
		totalSuccess += result.SuccessCount
		totalErrors += result.ErrorCount
	}

	fmt.Println("\n")
	fmt.Println("OVERALL SUMMARY:")
	fmt.Printf("   Total Requests: %d\n", totalRequests)
	fmt.Printf("   Total Successful: %d (%.1f%%)\n", totalSuccess,
		float64(totalSuccess)/float64(totalRequests)*100)
	fmt.Printf("   Total Errors: %d (%.1f%%)\n", totalErrors,
		float64(totalErrors)/float64(totalRequests)*100)
	fmt.Println("=")
}

func checkService() bool {
	fmt.Println("Checking if service is running...")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(BaseURL + "/health")

	if err != nil {
		fmt.Printf("Service is not running at %s\n", BaseURL)
		fmt.Println("Please start your docker-compose services first:")
		fmt.Println("  docker-compose up -d")
		return false
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("Service health check failed (status: %d)\n", resp.StatusCode)
		return false
	}

	fmt.Println("Service is running")
	return true
}

func main() {
	fmt.Println("URL Shortener Load Test Suite")
	fmt.Printf("Target: %s\n", BaseURL)
	fmt.Printf("Duration: %v per test\n", Duration)
	fmt.Printf("Workers: %d\n", Workers)
	fmt.Printf("Target Rate: %d requests/sec\n", RequestRate)
	fmt.Println()

	if !checkService() {
		os.Exit(1)
	}

	rand.Seed(time.Now().UnixNano())
	tester := NewLoadTester()

	var results []TestResult

	results = append(results, tester.runTest("Health Check", tester.testHealthCheck))
	results = append(results, tester.runTest("Home Page", tester.testHomePage))
	results = append(results, tester.runTest("URL Shortening", tester.testShortenURL))
	results = append(results, tester.runTest("List URLs", tester.testListURLs))

	tester.collectShortCodes()

	if len(shortCodes) > 0 {
		results = append(results, tester.runTest("URL Redirect", tester.testRedirect))
		results = append(results, tester.runTest("URL Stats", tester.testStats))
	} else {
		fmt.Println("Skipping redirect and stats test (no short codes available)")
	}

	printResults(results)

	fmt.Println("\nLoad test finished!")
}
