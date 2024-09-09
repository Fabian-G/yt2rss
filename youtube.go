package main

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"math"
	"mime"
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

type Option func(o options) options

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

type YoutubeAPIService struct {
	ApiKey   string
	Cache    *Cache
	ytClient *youtube.Service
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
	playlistId := "UULF" + channel.Id[2:] // Apparently UULF filters shorts
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
	y.Cache.InvalidateCacheIfDirty(playlistId, o.limit)
	client, err := y.client(ctx)
	if err != nil {
		return nil, err
	}
	call := client.PlaylistItems.List([]string{"contentDetails", "snippet"}).PlaylistId(playlistId)
	videos := make([]*feeds.Item, 0)
	for item, err := range take(o.limit, y.allPlaylistItems(ctx, call)) {
		if err != nil {
			return nil, err
		}
		video, err := y.mapToFeedItem(item, o)
		if err != nil {
			return nil, err
		}
		videos = append(videos, video)
		if y.Cache.HasItem(playlistId, video) {
			break
		}
	}
	y.Cache.Put(playlistId, videos...)
	after := videos[len(videos)-1].Created.Format(time.RFC3339)
	for item, err := range take(max(0, o.limit-len(videos)), y.Cache.Iter(playlistId, after)) {
		if err != nil {
			return nil, err
		}
		videos = append(videos, item)
	}
	y.Cache.UpdateMaxLimit(playlistId, o.limit)
	return videos, nil
}

func (y *YoutubeAPIService) mapToFeedItem(item *youtube.PlaylistItem, o options) (*feeds.Item, error) {
	published, err := time.Parse(time.RFC3339, item.Snippet.PublishedAt)
	if err != nil {
		return nil, fmt.Errorf("could not parse published date of video %s: %w", item.Id, err)
	}
	enclosure, err := y.formatEnclosure(item, o)
	if err != nil {
		return nil, fmt.Errorf("could not format enclosure url: %w", err)
	}
	return &feeds.Item{
		Title:       item.Snippet.Title,
		Link:        &feeds.Link{Href: fmt.Sprintf("https://youtube.com/watch?v=%s", item.Snippet.ResourceId.VideoId)},
		Id:          item.Snippet.ResourceId.VideoId,
		Created:     published,
		Description: item.Snippet.Description,
		Enclosure:   enclosure,
	}, nil
}

func (y *YoutubeAPIService) allPlaylistItems(ctx context.Context, call *youtube.PlaylistItemsListCall) iter.Seq2[*youtube.PlaylistItem, error] {
	return func(yield func(*youtube.PlaylistItem, error) bool) {
		cancel := errors.New("cancelled")
		err := call.Pages(ctx, func(pl *youtube.PlaylistItemListResponse) error {
			for _, e := range pl.Items {
				if !yield(e, nil) {
					return cancel
				}
			}
			return nil
		})
		if err != nil && !errors.Is(err, cancel) {
			yield(nil, err)
		}
	}
}

func (y *YoutubeAPIService) formatEnclosure(v *youtube.PlaylistItem, o options) (*feeds.Enclosure, error) {
	if len(o.enclosureBase) == 0 {
		return nil, nil
	}
	vId := v.Snippet.ResourceId.VideoId
	extension, err := mime.ExtensionsByType(o.mimetype)
	if err != nil {
		return nil, err
	}

	var enc *url.URL
	query := make(url.Values)
	if len(extension) == 0 {
		enc, err = url.Parse(fmt.Sprintf("%s/watch", o.enclosureBase))
		query.Add("v", vId)
	} else {
		enc, err = url.Parse(fmt.Sprintf("%s/watch/%s%s", o.enclosureBase, vId, extension[len(extension)-1]))
	}
	if err != nil {
		return nil, fmt.Errorf("could not parse enclosure base url: %w", err)
	}
	if o.format != defaultOptions.format {
		query.Add("format", o.format)
	}

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

func take[K any, V any](n int, seq iter.Seq2[K, V]) iter.Seq2[K, V] {
	var counter int
	return func(yield func(K, V) bool) {
		if n <= 0 {
			return
		}
		for k, v := range seq {
			if !yield(k, v) {
				break
			}
			counter++
			if counter >= n {
				break
			}
		}
	}
}
