package main

import (
	"encoding/json"
	"fmt"
	"iter"
	"log"
	"strconv"
	"time"

	"github.com/gorilla/feeds"
	"go.etcd.io/bbolt"
)

type Cache struct {
	*bbolt.DB
}

func (c *Cache) InvalidateCacheIfDirty(playlistId string, limit int) {
	if c == nil {
		return
	}
	err := c.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(playlistId))
		if b == nil {
			return nil
		}
		lastLimit, err := strconv.Atoi(string(b.Get([]byte("last-limit"))))
		if lastLimit < limit || err != nil {
			if err := tx.DeleteBucket([]byte(playlistId)); err != nil {
				return err
			}
		}
		newBucket, err := tx.CreateBucketIfNotExists([]byte(playlistId))
		if err != nil {
			return err
		}
		if err = newBucket.Put([]byte("last-limit"), []byte(strconv.Itoa(limit))); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.Printf("could not check if cache is dirty: %s\n", err)
	}
}

func (c *Cache) UpdateMaxLimit(playlistId string, limit int) {
	if c == nil {
		return
	}
	err := c.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(playlistId))
		if b == nil {
			return nil
		}
		lastLimit, err := strconv.Atoi(string(b.Get([]byte("last-limit"))))
		if err != nil {
			lastLimit = 0
		}
		if err = b.Put([]byte("last-limit"), []byte(strconv.Itoa(max(lastLimit, limit)))); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.Printf("could not update last-limit; %s\n", err)
	}
}

func (c *Cache) HasItem(playlistId string, item *feeds.Item) bool {
	if c == nil {
		return false
	}
	var result bool
	err := c.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(playlistId))
		if b == nil {
			result = false
			return nil
		}
		result = b.Get([]byte(fmt.Sprintf("%s-%s", item.Created.Format(time.RFC3339), item.Id))) != nil
		return nil
	})
	if err != nil {
		result = false
		log.Printf("Could not read cache: %s", err)
	}
	return result
}

func (c *Cache) Put(playlistId string, items ...*feeds.Item) {
	if c == nil {
		return
	}
	err := c.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(playlistId))
		if err != nil {
			return err
		}

		for _, item := range items {
			data, err := json.Marshal(item)
			if err != nil {
				return err
			}
			if err = b.Put([]byte(fmt.Sprintf("%s-%s", item.Created.Format(time.RFC3339), item.Id)), data); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("Could not write to cache: %s", err)
	}
}

func (c *Cache) Iter(playlistId string, after string) iter.Seq2[*feeds.Item, error] {
	return func(yield func(*feeds.Item, error) bool) {
		err := c.View(func(tx *bbolt.Tx) error {
			b := tx.Bucket([]byte(playlistId)).Cursor()
			if b == nil {
				return nil
			}
			_, _ = b.Seek([]byte(after))
			for k, v := b.Prev(); k != nil; k, v = b.Prev() {
				var item feeds.Item
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
