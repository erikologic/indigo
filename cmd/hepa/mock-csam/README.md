# Mock CSAM Detection Service

This is a simple mock CSAM detection server for integration with HEPA in development and test environments.

## Features
- Accepts image uploads via `/api/v1/check-csam` (POST, multipart/form-data)
- Returns `is_csam: true` for images whose SHA256 hash is in the configured list
- Uses a fixed token (`test-csam-token`) for authentication

## Usage

### Build
```
go build -o mock-csam main.go
```

### Run
```
MOCK_CSAM_PORT=8080 CSAM_HASHES="<hash1>,<hash2>" ./mock-csam
```

- `MOCK_CSAM_PORT`: Port to listen on (default: 8080)
- `CSAM_HASHES`: Comma-separated list of SHA256 hashes to treat as CSAM

### Docker Compose
This service is included in the `docker-compose.yml` alongside HEPA. HEPA should be configured to use:
- `HEPA_CSAM_SERVICE_URL=http://mock-csam:8080/api/v1/check-csam`
- `HEPA_CSAM_SERVICE_TOKEN=test-csam-token`

### How to get an image hash
```
sha256sum kids.jpg
```
Copy the hash and set it in `CSAM_HASHES`.

---
This mock is based on the integration test logic.
