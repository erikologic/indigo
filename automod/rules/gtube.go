package rules

import (
	"bytes"
	"strings"

	appbsky "github.com/bluesky-social/indigo/api/bsky"
	"github.com/bluesky-social/indigo/api/flashes"
	"github.com/bluesky-social/indigo/automod"
)

// https://en.wikipedia.org/wiki/GTUBE
var gtubeString = "XJS*C4JDBQADN1.NSBN3*2IDNEN*GTUBE-STANDARD-ANTI-UBE-TEST-EMAIL*C.34X"

var _ automod.PostRuleFunc = GtubePostRule

func GtubePostRule(c *automod.RecordContext, post *appbsky.FeedPost) error {
	if strings.Contains(post.Text, gtubeString) {
		c.AddRecordLabel("spam")
		c.Notify("slack")
		c.AddRecordTag("gtube-record")
	}
	return nil
}

var _ automod.ProfileRuleFunc = GtubeProfileRule

func GtubeProfileRule(c *automod.RecordContext, profile *appbsky.ActorProfile) error {
	if profile.Description != nil && strings.Contains(*profile.Description, gtubeString) {
		c.AddRecordLabel("spam")
		c.Notify("slack")
		c.AddAccountTag("gtuber-account")
	}
	return nil
}

var _ automod.RecordRuleFunc = GtubeFlashRule

func GtubeFlashRule(c *automod.RecordContext) error {
	if c.RecordOp.Collection.String() != "app.flashes.feed.post" {
		return nil
	}

	var flashPost flashes.FeedPost
	if err := flashPost.UnmarshalCBOR(bytes.NewReader(c.RecordOp.RecordCBOR)); err != nil {
		// If parsing fails, skip this record
		return nil
	}

	// Check for GTUBE string in the text
	if strings.Contains(flashPost.Text, gtubeString) {
		c.AddRecordLabel("spam")
		c.ReportRecord(automod.ReportReasonSpam, "GTube spam test pattern detected in flash post")
		c.Notify("slack")
		c.AddRecordTag("gtube-flash")
	}

	return nil
}
