package visual

import (
	"strings"

	"github.com/bluesky-social/indigo/automod"
	lexutil "github.com/bluesky-social/indigo/lex/util"
)

func (cc *CSAMClient) CSAMDetectionBlobRule(c *automod.RecordContext, blob lexutil.LexBlob, data []byte) error {
	// Only process image blobs
	if !strings.HasPrefix(blob.MimeType, "image/") && blob.MimeType != "*/*" {
		return nil
	}

	// Only process app.flashes.feed.post records for now
	if c.RecordOp.Collection.String() != "app.flashes.feed.post" {
		return nil
	}

	// Call external CSAM detection service
	resp, err := cc.CheckBlob(c.Ctx, blob, data)
	if err != nil {
		c.Logger.Error("CSAM detection service error", "err", err, "cid", blob.Ref.String())
		return nil // Don't fail processing on service errors
	}

	// Process the response
	if resp.IsCSAM {
		c.Logger.Info("CSAM detected by external service", "did", c.Account.Identity.DID, "uri", c.RecordOp.ATURI(), "confidence", resp.Confidence)
		
		// Add appropriate labels and tags
		c.AddRecordLabel("csam")
		c.AddRecordTag("external-csam-detection")
		c.AddAccountTag("csam-detected")
		
		// Notify moderators
		c.Notify("slack")
		
		// Report to moderation system
		c.ReportRecord(automod.ReportReasonOther, "CSAM detected by external service")
	} else {
		c.Logger.Debug("Content cleared by CSAM service", "cid", blob.Ref.String(), "confidence", resp.Confidence)
	}

	return nil
}