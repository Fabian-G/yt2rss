package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path"
	"strconv"

	"go.etcd.io/bbolt"
)

var (
	version        = "dev"
	commit         = "none"
	date           = "unknown"
	builtBy        = "unknown"
	yt2rssAsciiArt = `

 __     _________ ___  _____   _____ _____ 
 \ \   / /__   __|__ \|  __ \ / ____/ ____|
  \ \_/ /   | |     ) | |__) | (___| (___  
   \   /    | |    / /|  _  / \___ \\___ \ 
    | |     | |   / /_| | \ \ ____) |___) |
    |_|     |_|  |____|_|  \_\_____/_____/ 
                                           

`
)

func env(key, def string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return def
}

func intEnv(key string, def int) int {
	if val, ok := os.LookupEnv(key); ok {
		i, err := strconv.Atoi(val)
		if err != nil {
			log.Fatal(err)
		}
		return i
	}
	return def
}

func printVersion() {
	fmt.Println(yt2rssAsciiArt)
	fmt.Println("yt2rss: Bridge the youtube API to RSS")
	fmt.Println("https://fabian-g.github.io/yt2rss/")
	fmt.Println()
	fmt.Printf("Version: %s\n", version)
	fmt.Printf("Commit: %s\n", commit)
	fmt.Printf("BuildDate: %s\n", date)
	fmt.Printf("BuiltBy: %s\n", builtBy)
}

func main() {
	var limit int
	var ytdlCommand string
	var format string
	var mimeType string
	var apiKey string
	var baseUrl string
	var addr string
	var mode string
	var version bool

	flag.IntVar(&limit, "l", intEnv("YT2RSS_LIMIT", math.MaxInt), "Limit the number of items returned to l")
	flag.StringVar(&mode, "m", env("YT2RSS_MODE", "single"), "Serve for server mode and single for oneshot")
	flag.StringVar(&ytdlCommand, "c", env("YT2RSS_YTDL_COMMAND", "yt-dlp"), "The command to use to resolve enclosure URLs in serve mode")
	flag.StringVar(&format, "f", env("YT2RSS_FORMAT", "best[ext=mp4]"), "Format to download. This is directly passed to youtube-dl --format")
	flag.StringVar(&mimeType, "t", env("YT2RSS_MIME_TYPE", "video/mp4"), "The mime type to put into the enclosure tag")
	flag.StringVar(&apiKey, "k", env("YT2RSS_API_KEY", ""), "The API Key for the youtube data api v3")
	flag.StringVar(&baseUrl, "b", env("YT2RSS_BASE_URL", "http://localhost:9494"), "The url under which the server is reachable in serve mode")
	flag.StringVar(&addr, "p", env("YT2RSS_ADDR", ":9494"), "The addresse to bind")
	flag.BoolVar(&version, "v", false, "Print version info and exit")
	flag.Parse()

	if version {
		printVersion()
		os.Exit(0)
	}

	var svc YtSerice = &YoutubeAPIService{ApiKey: apiKey, Cache: openCache()}

	switch {
	case len(flag.Args()) == 0 && mode == "serve":
		server := Server{
			BaseUrl:     baseUrl,
			Limit:       limit,
			MimeType:    mimeType,
			YtdlCommand: ytdlCommand,
			Format:      format,
			Svc:         svc,
		}
		if err := server.Run(addr); err != nil {
			log.Fatal(err)
		}
	case len(flag.Args()) == 1 && mode == "single":
		channel, err := svc.Channel(context.Background(), flag.Arg(0),
			WithLimit(limit),
			WithFormat(format),
			WithMimeType(mimeType))
		if err != nil {
			log.Fatal(err)
		}
		if channel.WriteRss(os.Stdout) != nil {
			log.Fatal(err)
		}
	default:
		flag.Usage()
		os.Exit(1)
	}

}

func openCache() *Cache {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		log.Printf("Can not determine cache directory. Caching will be disabled: %s\n", err.Error())
		return nil
	}
	if err := os.Mkdir(path.Join(cacheDir, "yt2rss"), 0700); err != nil && !errors.Is(err, os.ErrExist) {
		log.Printf("Could not create cache directory. Caching will be disabled: %s\n", err.Error())
		return nil
	}
	cache, err := bbolt.Open(path.Join(cacheDir, "yt2rss", "cache.db"), 0600, nil)
	if err != nil {
		log.Printf("Could not open cache database. Caching will be disabled: %s\n", err.Error())
		return nil
	}

	return &Cache{DB: cache}
}
