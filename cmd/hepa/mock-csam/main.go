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
	// Optionally preload known CSAM images from env var (comma-separated hashes)
	if hashes := os.Getenv("CSAM_HASHES"); hashes != "" {
		for _, h := range splitAndTrim(hashes, ",") {
			csamImages[h] = true
		}
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
	token := r.Header.Get("Authorization")
	if token != "Bearer test-csam-token" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "bad form"})
		return
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "missing image"})
		return
	}
	defer file.Close()
	imageData, err := io.ReadAll(file)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "read error"})
		return
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(imageData))
	isCSAM := csamImages[hash]
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
