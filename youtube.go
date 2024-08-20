package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/feeds"
	"go.etcd.io/bbolt"
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
	Cache    *bbolt.DB
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

	videos := make([]*feeds.Item, 0)
	for item, err := range y.allPlaylistItems(ctx, playlistId, o.limit) {
		if err != nil {
			return nil, err
		}
		published, err := time.Parse(time.RFC3339, item.Snippet.PublishedAt)
		if err != nil {
			return nil, fmt.Errorf("could not parse published date of video %s: %w", item.Id, err)
		}
		enclosure, err := y.formatEnclosure(item, o)
		if err != nil {
			return nil, fmt.Errorf("could not format enclosure url: %w", err)
		}
		videos = append(videos, &feeds.Item{
			Title:       item.Snippet.Title,
			Link:        &feeds.Link{Href: fmt.Sprintf("https://youtube.com/watch?v=%s", item.Snippet.ResourceId.VideoId)},
			Id:          item.Snippet.ResourceId.VideoId,
			Created:     published,
			Description: item.Snippet.Description,
			Enclosure:   enclosure,
		})
	}
	return videos, nil
}

func (y *YoutubeAPIService) allPlaylistItems(ctx context.Context, playlistId string, limit int) iter.Seq2[*youtube.PlaylistItem, error] {
	y.invalidateCacheIfDirty(playlistId, limit)
	return func(yield func(*youtube.PlaylistItem, error) bool) {
		client, err := y.client(ctx)
		if err != nil {
			yield(nil, err)
			return
		}
		call := client.PlaylistItems.List([]string{"contentDetails", "snippet"}).PlaylistId(playlistId)
		var cancel error = errors.New("cancelled")
		var continueAt *youtube.PlaylistItem
		err = call.Pages(ctx, func(pl *youtube.PlaylistItemListResponse) error {
			for _, e := range pl.Items {
				if y.isCached(e) {
					continueAt = e
					return cancel
				}
				y.cache(e)
				limit--
				if !yield(e, nil) || limit <= 0 {
					return cancel
				}
			}
			return nil
		})
		if err != nil && !errors.Is(err, cancel) {
			yield(nil, err)
			return
		}
		if continueAt == nil {
			return
		}
		for item, err := range y.iterCache(playlistId, continueAt.Snippet.PublishedAt) {
			limit--
			if !yield(item, err) || limit <= 0 {
				return
			}
		}
	}
}

func (y *YoutubeAPIService) invalidateCacheIfDirty(playlistId string, limit int) {
	if y.Cache == nil {
		return
	}
	err := y.Cache.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(playlistId))
		if b == nil {
			return nil
		}
		countArr := b.Get([]byte("count"))
		var count uint32
		if countArr != nil {
			count = binary.LittleEndian.Uint32(countArr)
		}
		if count < uint32(limit) {
			if err := tx.DeleteBucket([]byte(playlistId)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("could not check if cache is dirty: %s\n", err)
	}
}

func (y *YoutubeAPIService) isCached(item *youtube.PlaylistItem) bool {
	if y.Cache == nil {
		return false
	}
	var result bool
	err := y.Cache.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(item.Snippet.PlaylistId))
		if b == nil {
			result = false
			return nil
		}
		result = b.Get([]byte(fmt.Sprintf("%s-%s", item.Snippet.PublishedAt, item.ContentDetails.VideoId))) != nil
		return nil
	})
	if err != nil {
		result = false
		log.Printf("Could not read cache: %s", err)
	}
	return result
}

func (y *YoutubeAPIService) cache(item *youtube.PlaylistItem) {
	if y.Cache == nil {
		return
	}
	err := y.Cache.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(item.Snippet.PlaylistId))
		if err != nil {
			return err
		}
		data, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if err = b.Put([]byte(fmt.Sprintf("%s-%s", item.Snippet.PublishedAt, item.ContentDetails.VideoId)), data); err != nil {
			return err
		}
		countArr := b.Get([]byte("count"))
		var count uint32
		if countArr != nil {
			count = binary.LittleEndian.Uint32(countArr)
		}
		incCount := [4]byte{}
		binary.LittleEndian.PutUint32(incCount[:], count+1)
		if err = b.Put([]byte("count"), incCount[:]); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.Printf("Could not write to cache: %s", err)
	}
}

func (y *YoutubeAPIService) iterCache(playlistId string, start string) iter.Seq2[*youtube.PlaylistItem, error] {
	return func(yield func(*youtube.PlaylistItem, error) bool) {
		err := y.Cache.View(func(tx *bbolt.Tx) error {
			b := tx.Bucket([]byte(playlistId)).Cursor()
			if b == nil {
				return nil
			}
			for k, v := b.Seek([]byte(start)); k != nil; k, v = b.Prev() {
				var item youtube.PlaylistItem
				if err := json.Unmarshal(v, &item); err != nil {
					return err
				}
				if !yield(&item, nil) {
					return nil
				}
			}
			return nil
		})
		if err != nil {
			yield(nil, err)
		}
	}
}
