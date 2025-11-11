package visual

import (
	"strings"
	"time"

	"github.com/bluesky-social/indigo/automod"
	lexutil "github.com/bluesky-social/indigo/lex/util"
)

func (sc *SpamHashClient) SpamHashBlobRule(c *automod.RecordContext, blob lexutil.LexBlob, data []byte) error {
	c.Logger.Debug("spam hash rule invoked", "did", c.Account.Identity.DID, "uri", c.RecordOp.ATURI(), "cid", blob.Ref.String(), "mimetype", blob.MimeType, "collection", c.RecordOp.Collection.String())

	// Only process image blobs
	if !strings.HasPrefix(blob.MimeType, "image/") && blob.MimeType != "*/*" {
		c.Logger.Debug("skipping non-image blob", "cid", blob.Ref.String(), "mimetype", blob.MimeType)
		return nil
	}

	collection := c.RecordOp.Collection.String()
	if collection != "app.flashes.story" {
		c.Logger.Debug("skipping non-flash record", "cid", blob.Ref.String(), "collection", collection)
		return nil
	}

	// Check blob against spam reference hash
	c.Logger.Info("checking blob for spam image", "cid", blob.Ref.String(), "size", len(data))

	start := time.Now()
	isSpam, distance, err := sc.CheckBlob(blob, data)
	duration := time.Since(start)
	spamHashCheckDuration.Observe(duration.Seconds())

	if err != nil {
		c.Logger.Error("spam hash check error", "err", err, "cid", blob.Ref.String())
		spamHashCheckCount.WithLabelValues("error").Inc()
		return nil // Don't fail processing on hash errors
	}

	// Process the response
	if isSpam {
		spamHashCheckCount.WithLabelValues("spam").Inc()
		c.Logger.Info("spam image detected",
			"did", c.Account.Identity.DID,
			"uri", c.RecordOp.ATURI(),
			"cid", blob.Ref.String(),
			"distance", distance,
			"threshold", sc.Threshold,
		)

		// Add appropriate labels and tags
		c.AddRecordLabel("spam")
		c.AddRecordTag("spam-image-detected")
		c.AddAccountTag("spam-image-posted")

		// Notify moderators
		c.Notify("slack")

		// Report to moderation system
		c.ReportRecord(automod.ReportReasonSpam, "Spam image detected via perceptual hash")
	} else {
		spamHashCheckCount.WithLabelValues("clean").Inc()
		c.Logger.Debug("image cleared by spam hash check", "cid", blob.Ref.String(), "distance", distance)
	}

	return nil
}
