# YT2RSS

YT2RSS (Youtube 2 RSS) bridges youtube channels to RSS.

## When to use

- You want to seamlessly subscribe to youtube channels from your favourite podcast player
- You also want to download and stream the videos using the integrated player of your podcatcher

## When not to use

Do not use if:
- You can live with the limitations of the standard rss feed youtube offers (limited to 15 videos, no download link, no profile picture)
- You don't want to obtain a youtube api key

## Caveats

- Do not expose this service publicly.
- Subscribing to channels with many videos is naturally slow. It is therefore recommended to limit the number of items using the `-l` flag or the `limit` query parameter after updating once without limit.
- Extracting a downloadable url for a youtube video relies on `yt-dlp`. This can be slow and fragile at times.
- Downloading videos with separate audio and video streams is not supported. Therefore for many videos the maximum resolution is very limited.

## Installation

Download the binary from the release page or install with
```bash
go install github.com/Fabian-G/yt2rss@latest
```

You will also need to install [yt-dlp](https://github.com/yt-dlp/yt-dlp).

## Setup 

1. Obtain a Youtube API Key from [here](https://console.developers.google.com/start/api?id=youtube&hl=de) (I highly recommend to use a throwaway google account for this).
2. Start yt2rss: `yt2rss -m serve -b 'http://<your-domain-here>:9494' -k "<your-api-key-here>"`
3. Subscribe to any youtube channel using the @handle or channel id (e.g. `http://<your-domain-here>:9494/@ThePrimeTimeagen`)
4. Download or stream videos like you would with any podcast

## Query Params

When subscribing to a channel the following query parameters are supported:
- **limit=n**: Limits the number of items in the rss feed to n
- **format=f**: The format to use. This is directly passed to `yt-dlp`. Formats where the audio and video channel are separate streams are not supported. You can use this to get an audio only version, though (e.g. "bestaudio[ext=mp4]")
- **mimeType**: The mime type used in the enclosure tag. Set this to match your format.

## Environment Variables

All command line flags can be set through environment variables as well:
- `YT2RSS_LIMIT`
- `YT2RSS_MODE`
- `YT2RSS_YTDL_COMMAND`
- `YT2RSS_FORMAT`
- `YT2RSS_MIME_TYPE`
- `YT2RSS_API_KEY`
- `YT2RSS_BASE_URL`
- `YT2RSS_ADDR`

## Single mode

With single mode you can use yt2rss for example in newsboat `exec` directives:

```bash
# .config/newsboat/urls
"exec:yt2rss -m single yt2rss -k "<your-api-key>" @ThePrimeTimeagen"
```

