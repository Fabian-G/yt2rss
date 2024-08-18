package yt2rss

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/feeds"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
)

type YtSerice interface {
	Channel(ctx context.Context, id string, o ...Option) (*feeds.Feed, error)
}

var limitReached error = errors.New("limit reached")

type options struct {
	limit         int
	format        string
	mimetype      string
	enclosureBase string
}

var defaultOptions options = options{
	limit:         math.MaxInt,
	format:        "best[ext=mp4]",
	mimetype:      "video/mp4",
	enclosureBase: "",
}

func WithLimit(limit int) func(o options) options {
	return func(o options) options {
		o.limit = limit
		return o
	}
}

func WithFormat(format string) func(o options) options {
	return func(o options) options {
		o.format = format
		return o
	}
}

func WithMimeType(mime string) func(o options) options {
	return func(o options) options {
		o.mimetype = mime
		return o
	}
}

func WithEnclosureBase(base string) func(o options) options {
	return func(o options) options {
		o.enclosureBase = base
		return o
	}
}

type Option func(o options) options

type YoutubeAPIService struct {
	ApiKey   string
	ytClient *youtube.Service
}

func (y *YoutubeAPIService) formatEnclosure(v *youtube.PlaylistItem, o options) (*feeds.Enclosure, error) {
	if len(o.enclosureBase) == 0 {
		return nil, nil
	}
	enc, err := url.Parse(fmt.Sprintf("%s/watch", o.enclosureBase))
	if err != nil {
		return nil, fmt.Errorf("could not parse enclosure base url: %w", err)
	}
	query := enc.Query()
	if o.format != defaultOptions.format {
		query.Add("format", o.format)
	}
	query.Add("v", v.Snippet.ResourceId.VideoId)

	enc.RawQuery = query.Encode()

	return &feeds.Enclosure{Url: enc.String(), Length: "-1", Type: o.mimetype}, nil
}

func (y *YoutubeAPIService) client(ctx context.Context) (*youtube.Service, error) {
	if y.ytClient != nil {
		return y.ytClient, nil
	}
	client, err := youtube.NewService(ctx, option.WithAPIKey(y.ApiKey))
	if err != nil {
		return nil, fmt.Errorf("could not create youtube client: %w", err)
	}
	y.ytClient = client
	return y.ytClient, nil
}

func (y *YoutubeAPIService) Channel(ctx context.Context, id string, o ...Option) (*feeds.Feed, error) {
	options := defaultOptions
	for _, opt := range o {
		options = opt(options)
	}
	client, err := y.client(ctx)
	if err != nil {
		return nil, err
	}

	channelCall := client.Channels.List([]string{"contentDetails", "snippet"})
	if strings.HasPrefix(id, "@") {
		channelCall.ForHandle(id)
	} else {
		channelCall.Id(id)
	}

	channelResponse, err := channelCall.Do()
	if err != nil {
		return nil, fmt.Errorf("could not read channel details: %w", err)
	}
	if len(channelResponse.Items) != 1 {
		return nil, fmt.Errorf("could not find channel with id %s", id)
	}
	channel := channelResponse.Items[0]
	playlistId := channel.ContentDetails.RelatedPlaylists.Uploads
	videos, err := y.videos(ctx, playlistId, options)
	if err != nil {
		return nil, fmt.Errorf("could not load videos from channel %s: %w", id, err)
	}

	thumbnail := channel.Snippet.Thumbnails.Default
	return &feeds.Feed{
		Title:       channel.Snippet.Title,
		Image:       &feeds.Image{Url: thumbnail.Url, Width: int(thumbnail.Width), Height: int(thumbnail.Height)},
		Id:          channel.Id,
		Link:        &feeds.Link{Href: fmt.Sprintf("https://youtube.com/channel/%s", channel.Id)},
		Items:       videos,
		Description: channel.Snippet.Description,
	}, nil
}

func (y *YoutubeAPIService) videos(ctx context.Context, playlistId string, o options) ([]*feeds.Item, error) {
	client, err := y.client(ctx)
	if err != nil {
		return nil, err
	}

	videos := make([]*feeds.Item, 0)
	err = client.PlaylistItems.List([]string{"contentDetails", "snippet"}).
		PlaylistId(playlistId).Pages(ctx, func(pl *youtube.PlaylistItemListResponse) error {
		for _, e := range pl.Items {
			published, err := time.Parse(time.RFC3339, e.Snippet.PublishedAt)
			if err != nil {
				return fmt.Errorf("could not parse published date of video %s: %w", e.Id, err)
			}
			enclosure, err := y.formatEnclosure(e, o)
			if err != nil {
				return fmt.Errorf("could not format enclosure url: %w", err)
			}
			videos = append(videos, &feeds.Item{
				Title:       e.Snippet.Title,
				Link:        &feeds.Link{Href: fmt.Sprintf("https://youtube.com/watch?v=%s", e.Snippet.ResourceId.VideoId)},
				Id:          e.Snippet.ResourceId.VideoId,
				Created:     published,
				Description: e.Snippet.Description,
				Enclosure:   enclosure,
			})
		}
		if len(videos) >= o.limit {
			videos = videos[:o.limit]
			return limitReached
		}
		return nil
	})
	if err != nil && !errors.Is(err, limitReached) {
		return nil, err
	}
	return videos, nil
}
