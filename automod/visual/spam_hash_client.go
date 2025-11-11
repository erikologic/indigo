package visual

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"os"

	"github.com/corona10/goimagehash"
	lexutil "github.com/bluesky-social/indigo/lex/util"
)

type SpamHashClient struct {
	ReferenceHash *goimagehash.ImageHash
	Threshold     int
	ReferencePath string
}

func NewSpamHashClient(referenceImagePath string, threshold int) (*SpamHashClient, error) {
	if threshold <= 0 {
		threshold = 15 // Default threshold: 15 bits difference
	}

	// Load and hash the reference spam image
	file, err := os.Open(referenceImagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open reference image %s: %w", referenceImagePath, err)
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("failed to decode reference image %s: %w", referenceImagePath, err)
	}

	// Use average hash for best discrimination
	hash, err := goimagehash.AverageHash(img)
	if err != nil {
		return nil, fmt.Errorf("failed to compute hash for reference image %s: %w", referenceImagePath, err)
	}

	slog.Info("spam hash client initialized", "reference_image", referenceImagePath, "hash", hash.ToString(), "threshold", threshold)

	return &SpamHashClient{
		ReferenceHash: hash,
		Threshold:     threshold,
		ReferencePath: referenceImagePath,
	}, nil
}

func (sc *SpamHashClient) CheckBlob(blob lexutil.LexBlob, blobBytes []byte) (bool, int, error) {
	// Decode the blob image
	img, _, err := image.Decode(bytes.NewReader(blobBytes))
	if err != nil {
		return false, 0, fmt.Errorf("failed to decode blob image: %w", err)
	}

	// Compute average hash for the blob
	blobHash, err := goimagehash.AverageHash(img)
	if err != nil {
		return false, 0, fmt.Errorf("failed to compute hash for blob: %w", err)
	}

	// Calculate Hamming distance between reference and blob hash
	distance, err := sc.ReferenceHash.Distance(blobHash)
	if err != nil {
		return false, 0, fmt.Errorf("failed to calculate hash distance: %w", err)
	}

	isSpam := distance <= sc.Threshold

	slog.Debug("spam hash check",
		"cid", blob.Ref.String(),
		"blob_hash", blobHash.ToString(),
		"reference_hash", sc.ReferenceHash.ToString(),
		"distance", distance,
		"threshold", sc.Threshold,
		"is_spam", isSpam,
	)

	return isSpam, distance, nil
}
