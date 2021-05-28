package main

import (
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"barista.run/bar"
	barista "barista.run/modules/media"
	pb "github.com/gidoBOSSftw5731/ProjectEdison/server/edison_proto"
	"github.com/gidoBOSSftw5731/log"
	"github.com/gorilla/websocket"
	"github.com/jinzhu/configor"
)

var config = struct {
	// LogDepth defines the verbosity of the logs. The log package
	// details the appropriate values
	LogDepth int `default:"4"`

	// ListenAddr is the address that will be listened for HTTP requests. It defaults to
	// 127.0.0.1:8080
	ListenAddr string `default:"0.0.0.0:8080"`
}{}

var (
	upgrader    = websocket.Upgrader{} // use default options
	musicPlayer *barista.AutoModule
	musicInfo   barista.Info
)

func main() {
	log.SetCallDepth(4)

	err := configor.Load(&config, "../config.yml")
	if err != nil {
		log.Panicln(err)
	}
	log.SetCallDepth(config.LogDepth)

	musicPlayer = barista.Auto()

	// the "repeated output" function seems to require the i3 bar exist, which it doesn't,
	// so instead of faking it I just do this which is pretty much what the normal func does
	// anyway except this one should work even if the music isn't paused.
	go func() {
		for {
			musicPlayer = musicPlayer.Output(musicData)
			musicPlayer.Stream(sink)
			time.Sleep(500 * time.Millisecond)
		}
	}()

	startHTTPListener()
}

//boilerplate to make the http package happy
type httpHandler struct{}

// startHTTPListener is intended to run at startup and will listen on the specified address
// and port for requests for files or for API data. API schema detailed in ServeHTTP
func startHTTPListener() {
	log.Traceln("Starting HTTP server")

	mux := http.NewServeMux()
	mux.Handle("/", &httpHandler{})

	err := http.ListenAndServe(config.ListenAddr, mux)
	if err != nil {
		log.Fatalln(err)
	}

}

func (*httpHandler) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	urlPath := strings.Split(req.URL.Path, "/")
	// if request is for the API then process it as an api request
	// if len = 2 then there is nothing after /api in which case I don't care
	if strings.HasPrefix(req.URL.Path, "/api") && len(urlPath) > 2 {
		//log.Traceln("API request made for ", strings.Split(req.URL.Path, "/"), req.URL.Path)
		switch urlPath[2] {
		case "ws":
			initWebSocket(resp, req)
		case "musicconnected":
			p := pb.MusicStatus{
				PlayerName: musicInfo.PlayerName,   
				PlaybackStatus:  musicInfo.PlaybackStatus
				Length: musicInfo.Length.Unix(),
				Title: musicInfo.Title,
				Artist:  musicInfo.Artist,
				Album: musicInfo.Album,
				AlbumArtist: musicInfo.AlbumArtist,
			}
			fmt.Fprintf(resp, "%#v", musicInfo)
		default:
			log.Debugln("default case, TODO: implement error")
		}
		return
	}

	// redirect / to index.html silently, this is a short term solution.
	if req.URL.Path == "/" {
		req.URL.Path = "index.html"
	}

	// serve files from src
	http.ServeFile(resp, req, path.Join("src/", req.URL.Path))
}

func initWebSocket(resp http.ResponseWriter, req *http.Request) {
	// Upgrade our raw HTTP connection to a websocket based one
	conn, err := upgrader.Upgrade(resp, req, nil)
	if err != nil {
		log.Errorln("Error during connection upgradation: ", err)
		return
	}
	defer conn.Close()

	// The event loop
EventLoop:
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			log.Errorln("Error during message reading: ", err)
			break
		}

		switch messageType {
		// conn close
		case 2:
			log.Debugln("ws connection closed")
			break EventLoop
		// text
		case 0:
			log.Tracef("Received: %s", message)
			err = conn.WriteMessage(messageType, message)
			if err != nil {
				log.Errorln("Error during message writing: ", err)
				break EventLoop
			}
		// binary
		case 1:
			log.Errorln("Binary data recieved but nothing written to handle binary!")
		}

	}
}

// throwaway sink because barista is meant for i3 bars and all I want is to cannibalize
// it for its mpris functionality
func sink(o bar.Output) {
	// log.Tracef("Sink output: %#v", o)
}

func musicData(info barista.Info) bar.Output {
	musicInfo = info
	return nil
}
