package visual

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"time"

	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/bluesky-social/indigo/util"

	"github.com/carlmjohnson/versioninfo"
)

type CSAMClient struct {
	Client   http.Client
	ApiToken string
	Host     string
}

// Response structure for CSAM detection service
type CSAMResp struct {
	IsCSAM     bool    `json:"is_csam"`
	Confidence float64 `json:"confidence"`
	Message    string  `json:"message,omitempty"`
}

func NewCSAMClient(host, token string) *CSAMClient {
	return &CSAMClient{
		Client:   *util.RobustHTTPClient(),
		ApiToken: token,
		Host:     host,
	}
}

func (cc *CSAMClient) CheckBlob(ctx context.Context, blob lexutil.LexBlob, blobBytes []byte) (*CSAMResp, error) {
	slog.Info("CSAM client: sending blob to detection service", "cid", blob.Ref.String(), "mimetype", blob.MimeType, "size", len(blobBytes), "host", cc.Host)

	// Create multipart form for image upload
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("image", blob.Ref.String())
	if err != nil {
		return nil, err
	}
	_, err = part.Write(blobBytes)
	if err != nil {
		return nil, err
	}
	err = writer.Close()
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/api/v1/check-csam", cc.Host)
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	defer func() {
		duration := time.Since(start)
		csamAPIDuration.Observe(duration.Seconds())
	}()

	// Set authorization header with JWT token
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cc.ApiToken))
	req.Header.Add("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "indigo-automod/"+versioninfo.Short())

	req = req.WithContext(ctx)
	res, err := cc.Client.Do(req)
	if err != nil {
		slog.Error("CSAM client: API request failed", "cid", blob.Ref.String(), "err", err)
		return nil, fmt.Errorf("CSAM API request failed: %v", err)
	}
	defer res.Body.Close()

	csamAPICount.WithLabelValues(fmt.Sprint(res.StatusCode)).Inc()
	if res.StatusCode != 200 {
		slog.Error("CSAM client: API returned non-200", "cid", blob.Ref.String(), "status", res.StatusCode)
		return nil, fmt.Errorf("CSAM API request failed statusCode=%d", res.StatusCode)
	}

	respBytes, err := io.ReadAll(res.Body)
	if err != nil {
		slog.Error("CSAM client: failed to read API response body", "cid", blob.Ref.String(), "err", err)
		return nil, fmt.Errorf("failed to read CSAM API resp body: %v", err)
	}

	var respObj CSAMResp
	if err := json.Unmarshal(respBytes, &respObj); err != nil {
		slog.Error("CSAM client: failed to parse API response JSON", "cid", blob.Ref.String(), "err", err, "body", string(respBytes))
		return nil, fmt.Errorf("failed to parse CSAM API resp JSON: %v", err)
	}

	slog.Info("CSAM client: API response", "cid", blob.Ref.String(), "is_csam", respObj.IsCSAM, "confidence", respObj.Confidence, "message", respObj.Message)
	return &respObj, nil
}
