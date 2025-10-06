package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

// Map of image hashes to CSAM status (true = CSAM, false = not CSAM)
var csamImages = map[string]bool{}

func main() {
	// Require known CSAM images from env var (comma-separated hashes)
	hashes := os.Getenv("CSAM_HASHES")
	if hashes == "" {
		log.Fatal("CSAM_HASHES environment variable must be set and non-empty")
	}
	for _, h := range splitAndTrim(hashes, ",") {
		csamImages[h] = true
		log.Printf("Configured CSAM hash: %s", h)
	}

	http.HandleFunc("/api/v1/check-csam", handleCheckCSAM)
	port := os.Getenv("MOCK_CSAM_PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Mock CSAM server listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func handleCheckCSAM(w http.ResponseWriter, r *http.Request) {
	log.Printf("Received request: %s %s", r.Method, r.URL.Path)
	token := r.Header.Get("Authorization")
	if token != "Bearer test-csam-token" {
		log.Printf("Unauthorized request: token=%q", token)
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		log.Printf("Bad form: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "bad form"})
		return
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		log.Printf("Missing image: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "missing image"})
		return
	}
	defer file.Close()
	imageData, err := io.ReadAll(file)
	if err != nil {
		log.Printf("Read error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "read error"})
		return
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(imageData))
	isCSAM := csamImages[hash]
	log.Printf("Checked image hash: %s, isCSAM: %v", hash, isCSAM)
	resp := map[string]interface{}{
		"is_csam":    isCSAM,
		"confidence": 0.95,
		"message":    fmt.Sprintf("Mock detection result for hash %s", hash[:8]),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func splitAndTrim(s, sep string) []string {
	var out []string
	for _, part := range strings.Split(s, sep) {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
