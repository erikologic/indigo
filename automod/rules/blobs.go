package rules

import (
	"bytes"
	_ "embed"

	"github.com/bluesky-social/indigo/automod"
	lexutil "github.com/bluesky-social/indigo/lex/util"
)

//go:embed kids.jpg
var kidsImageData []byte

var _ automod.BlobRuleFunc = BlobVerifyRule

func BlobVerifyRule(c *automod.RecordContext, blob lexutil.LexBlob, data []byte) error {

	if len(data) == 0 {
		c.AddRecordFlag("empty-blob")
	}

	// check size
	if blob.Size >= 0 && int64(len(data)) != blob.Size {
		c.AddRecordFlag("invalid-blob")
	} else {
		c.Logger.Info("blob checks out", "cid", blob.Ref, "size", blob.Size, "mimetype", blob.MimeType)
	}

	return nil
}

var _ automod.BlobRuleFunc = ImageMatchTestRule

// ImageMatchTestRule detects if an image blob matches testing/kids.jpg for CSAM testing
func ImageMatchTestRule(c *automod.RecordContext, blob lexutil.LexBlob, data []byte) error {
	// Only process app.bsky.feed.post records
	if c.RecordOp.Collection.String() != "app.bsky.feed.post" {
		return nil
	}

	// Only process image blobs (including */* for testing)
	if blob.MimeType != "image/jpeg" && blob.MimeType != "image/jpg" && blob.MimeType != "*/*" {
		return nil
	}

	// Compare blob data with embedded kids.jpg reference
	if bytes.Equal(data, kidsImageData) {
		c.AddRecordLabel("csam")
		c.AddRecordTag("image-match-test")
		c.AddAccountTag("csam-detected")
		c.Notify("slack")
	} 

	return nil
}
