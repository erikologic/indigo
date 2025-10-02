package visual

import (
	"strings"

	"github.com/bluesky-social/indigo/automod"
	lexutil "github.com/bluesky-social/indigo/lex/util"
)

func (cc *CSAMClient) CSAMDetectionBlobRule(c *automod.RecordContext, blob lexutil.LexBlob, data []byte) error {
	c.Logger.Info("CSAM rule invoked", "did", c.Account.Identity.DID, "uri", c.RecordOp.ATURI(), "cid", blob.Ref.String(), "mimetype", blob.MimeType, "collection", c.RecordOp.Collection.String())
	// Only process image blobs
	if !strings.HasPrefix(blob.MimeType, "image/") && blob.MimeType != "*/*" {
		c.Logger.Debug("Skipping non-image blob", "cid", blob.Ref.String(), "mimetype", blob.MimeType)
		return nil
	}

	// Only process app.flashes.feed.post records for now
	if c.RecordOp.Collection.String() != "app.flashes.feed.post" {
		c.Logger.Debug("Skipping non-flash post", "cid", blob.Ref.String(), "collection", c.RecordOp.Collection.String())
		return nil
	}

	// Call external CSAM detection service
	c.Logger.Info("Calling CSAM detection service", "cid", blob.Ref.String(), "size", len(data))
	resp, err := cc.CheckBlob(c.Ctx, blob, data)
	if err != nil {
		c.Logger.Error("CSAM detection service error", "err", err, "cid", blob.Ref.String())
		return nil // Don't fail processing on service errors
	}

	// Process the response
	if resp.IsCSAM {
		c.Logger.Info("CSAM detected by external service", "did", c.Account.Identity.DID, "uri", c.RecordOp.ATURI(), "cid", blob.Ref.String(), "confidence", resp.Confidence, "message", resp.Message)
		// Add appropriate labels and tags
		c.AddRecordLabel("csam")
		c.AddRecordTag("external-csam-detection")
		c.AddAccountTag("csam-detected")
		// Notify moderators
		c.Notify("slack")
		// Report to moderation system using new Ozone reason type
		c.ReportRecord("tools.ozone.report.defs#reasonChildSafetyCSAM", "[automod] CSAM detected by external service")
	} else {
		c.Logger.Debug("Content cleared by CSAM service", "cid", blob.Ref.String(), "confidence", resp.Confidence, "message", resp.Message)
	}

	return nil
}
