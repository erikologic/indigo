package flashes

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/bluesky-social/indigo/lex/util"
	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/assert"
)

func TestStoryUnmarshalJSON(t *testing.T) {
	jsonStr := `{
		"$type": "app.flashes.story",
		"createdAt": "2025-11-11T14:16:39Z",
		"text": "Test story",
		"embed": {
			"images": [{
				"alt": "Test image",
				"image": {
					"$type": "blob",
					"ref": {"$link": "bafkreiey6rii333s3uu5rwqo44mcwgoyk4xfougap4p6mgish4yu2reuri"},
					"mimeType": "image/jpeg",
					"size": 11878
				}
			}]
		}
	}`

	var story Story
	err := json.Unmarshal([]byte(jsonStr), &story)
	assert.NoError(t, err)

	assert.Equal(t, "app.flashes.story", story.LexiconTypeID)
	assert.Equal(t, "Test story", story.Text)
	assert.NotNil(t, story.Embed)
	assert.Len(t, story.Embed.Images, 1)
	assert.Equal(t, "Test image", story.Embed.Images[0].Alt)
	assert.NotNil(t, story.Embed.Images[0].Image)
	assert.Equal(t, "bafkreiey6rii333s3uu5rwqo44mcwgoyk4xfougap4p6mgish4yu2reuri", story.Embed.Images[0].Image.Ref.String())
}

func TestStoryUnmarshalViaLexDecoder(t *testing.T) {
	jsonStr := `{
		"$type": "app.flashes.story",
		"createdAt": "2025-11-11T14:16:39Z",
		"text": "Test story",
		"embed": {
			"images": [{
				"alt": "Test image",
				"image": {
					"$type": "blob",
					"ref": {"$link": "bafkreiey6rii333s3uu5rwqo44mcwgoyk4xfougap4p6mgish4yu2reuri"},
					"mimeType": "image/jpeg",
					"size": 11878
				}
			}]
		}
	}`

	var decoder util.LexiconTypeDecoder
	err := json.Unmarshal([]byte(jsonStr), &decoder)
	assert.NoError(t, err)

	story, ok := decoder.Val.(*Story)
	assert.True(t, ok, "Expected Story type")
	assert.Equal(t, "app.flashes.story", story.LexiconTypeID)
	assert.Equal(t, "Test story", story.Text)
	assert.NotNil(t, story.Embed, "Embed should not be nil")
	if story.Embed != nil {
		assert.Len(t, story.Embed.Images, 1)
		assert.Equal(t, "Test image", story.Embed.Images[0].Alt)
		assert.NotNil(t, story.Embed.Images[0].Image)
	}
}

func TestStoryUnmarshalViaLexDecoderExactPDSFormat(t *testing.T) {
	// This is the exact format being sent to the PDS
	jsonStr := `{
		"$type": "app.flashes.story",
		"createdAt": "2025-11-11T14:16:39Z",
		"text": "Test story",
		"embed": {
			"images": [{
				"alt": "Test image",
				"image": {
					"$type": "blob",
					"ref": {"$link": "bafkreiey6rii333s3uu5rwqo44mcwgoyk4xfougap4p6mgish4yu2reuri"},
					"mimeType": "*/*",
					"size": 11878
				}
			}]
		}
	}`

	// Test via LexiconTypeDecoder like the PDS does
	var decoder util.LexiconTypeDecoder
	err := json.Unmarshal([]byte(jsonStr), &decoder)
	if err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	story, ok := decoder.Val.(*Story)
	if !ok {
		t.Fatalf("Expected Story type, got %T", decoder.Val)
	}

	t.Logf("Story: Text=%s, Embed=%v", story.Text, story.Embed != nil)
	if story.Embed != nil {
		t.Logf("Embed.Images len=%d", len(story.Embed.Images))
		if len(story.Embed.Images) > 0 {
			t.Logf("Image[0]: Alt=%s, Image=%v", story.Embed.Images[0].Alt, story.Embed.Images[0].Image != nil)
		}
	}

	assert.NotNil(t, story.Embed, "Embed should not be nil!")
}

func TestStoryCBORRoundtrip(t *testing.T) {
	// Create a story with an embed
	story := &Story{
		LexiconTypeID: "app.flashes.story",
		CreatedAt:     "2025-11-11T14:16:39Z",
		Text:          "Test story",
		Embed: &Story_ImageEmbed{
			Images: []*Story_Image{
				{
					Alt: "Test image",
					Image: &util.LexBlob{
						Ref:      util.LexLink(mustParseCID("bafkreiey6rii333s3uu5rwqo44mcwgoyk4xfougap4p6mgish4yu2reuri")),
						MimeType: "*/*",
						Size:     11878,
					},
				},
			},
		},
	}

	// Marshal to CBOR
	var buf bytes.Buffer
	err := story.MarshalCBOR(&buf)
	assert.NoError(t, err)

	t.Logf("CBOR bytes: %d", buf.Len())
	t.Logf("CBOR hex: %x", buf.Bytes())

	// Unmarshal from CBOR
	var story2 Story
	err = story2.UnmarshalCBOR(&buf)
	if err != nil {
		t.Fatalf("Unmarshal CBOR error: %v", err)
	}
	assert.NoError(t, err)

	t.Logf("After unmarshal: Text=%s, Embed=%v", story2.Text, story2.Embed != nil)
	if story2.Embed != nil {
		t.Logf("Embed.Images len=%d", len(story2.Embed.Images))
	}

	// Verify
	assert.Equal(t, story.Text, story2.Text)
	assert.NotNil(t, story2.Embed, "Embed should not be nil after CBOR roundtrip!")
	if story2.Embed != nil {
		assert.Len(t, story2.Embed.Images, 1)
		assert.Equal(t, "Test image", story2.Embed.Images[0].Alt)
	}
}

func mustParseCID(s string) cid.Cid {
	c, err := cid.Decode(s)
	if err != nil {
		panic(err)
	}
	return c
}
