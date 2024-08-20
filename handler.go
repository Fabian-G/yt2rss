package main

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"time"

	"github.com/coreos/go-systemd/activation"
)

type Server struct {
	BaseUrl     string
	Limit       int
	MimeType    string
	YtdlCommand string
	Format      string
	Svc         YtSerice
}

func (s *Server) channel(rw http.ResponseWriter, r *http.Request) {
	log.Printf("%s %s\n", r.Method, r.URL)
	channelId := r.PathValue("channel")
	if len(channelId) == 0 {
		http.Error(rw, "channel id must not be empty", http.StatusBadRequest)
		return
	}
	query := r.URL.Query()
	limit, err := strconv.Atoi(query.Get("limit"))
	if err != nil || limit == 0 {
		limit = s.Limit
	}

	mimeType := query.Get("mimeType")
	if len(mimeType) == 0 {
		mimeType = s.MimeType
	}

	format := query.Get("format")
	if len(format) == 0 {
		format = s.Format
	}

	channel, err := s.Svc.Channel(r.Context(), channelId,
		WithLimit(limit),
		WithFormat(format),
		WithEnclosureBase(s.BaseUrl),
		WithMimeType(mimeType))
	if err != nil {
		http.Error(rw, "could not read channel information", http.StatusBadRequest)
		log.Println(err)
		return
	}

	if err := channel.WriteRss(rw); err != nil {
		log.Println(err)
		http.Error(rw, "could not map channel to rss", http.StatusInternalServerError)
		return
	}
}

func (s *Server) watch(rw http.ResponseWriter, r *http.Request) {
	log.Printf("%s %s\n", r.Method, r.URL)
	query := r.URL.Query()
	vId := query.Get("v")
	if len(vId) == 0 {
		http.Error(rw, "missing video id", http.StatusBadRequest)
		return
	}
	format := query.Get("format")
	if len(format) == 0 {
		format = s.Format
	}
	url, err := s.getUrl(vId, format)
	if err != nil {
		http.Error(rw, "extracting video url failed", http.StatusInternalServerError)
		log.Println(err)
		return
	}
	http.Redirect(rw, r, url, http.StatusTemporaryRedirect)
}

func (s *Server) Run(addr string) error {
	sm := http.NewServeMux()
	sm.HandleFunc("GET /{channel}", s.channel)
	sm.HandleFunc("GET /watch", s.watch)
	sockets, err := activation.Listeners()
	if err != nil {
		log.Printf("Warning: Error occurred while obtaining systemd sockets: %s", err)
	}
	var socket net.Listener
	if len(sockets) == 0 {
		socket, err = net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("could not create network socket: %s", err)
		}
	} else if len(sockets) == 1 {
		socket = sockets[0]
	} else {
		return fmt.Errorf("got %d sockets expected 1", len(sockets))
	}
	httpServer := &http.Server{
		Handler:     sm,
		ReadTimeout: 120 * time.Second,
	}
	return httpServer.Serve(socket)
}

func (s *Server) getUrl(videoId string, format string) (string, error) {
	url := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	args := []string{"--get-url", fmt.Sprintf("--format=%s", format), fmt.Sprintf("https://youtube.com/watch?v=%s", videoId)}
	cmd := exec.Command(s.YtdlCommand, args...)
	cmd.Stdout = url
	cmd.Stderr = errBuf
	if err := cmd.Run(); err != nil {
		log.Println(errBuf.String())
		return "", err
	}
	return url.String(), nil

}
