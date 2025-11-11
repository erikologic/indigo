// app.flashes.story lexicon type

package flashes

import (
	"github.com/bluesky-social/indigo/lex/util"
)

func init() {
	util.RegisterType("app.flashes.story", &Story{})
}

// RECORDTYPE: Story
type Story struct {
	LexiconTypeID string            `json:"$type,const=app.flashes.story" cborgen:"$type,const=app.flashes.story"`
	CreatedAt     string            `json:"createdAt" cborgen:"createdAt"`
	Embed         *Story_ImageEmbed `json:"embed,omitempty" cborgen:"embed,omitempty"`
	Text          string            `json:"text" cborgen:"text"`
}

// Story_ImageEmbed is the embed type for Story
type Story_ImageEmbed struct {
	Images []*Story_Image `json:"images" cborgen:"images"`
}

// Story_Image is an image in a Story
type Story_Image struct {
	Alt         string             `json:"alt" cborgen:"alt"`
	AspectRatio *Story_AspectRatio `json:"aspectRatio,omitempty" cborgen:"aspectRatio,omitempty"`
	Image       *util.LexBlob      `json:"image" cborgen:"image"`
}

// Story_AspectRatio is the aspect ratio for an image
type Story_AspectRatio struct {
	Height int64 `json:"height" cborgen:"height"`
	Width  int64 `json:"width" cborgen:"width"`
}
