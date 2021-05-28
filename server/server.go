package main

import (
	"net/http"
	"path"
	"strings"

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
	ListenAddr string `default:"127.0.0.1:8080"`
}{}

var upgrader = websocket.Upgrader{} // use default options

func main() {
	log.SetCallDepth(4)

	err := configor.Load(&config, "../config.yml")
	if err != nil {
		log.Panicln(err)
	}
	log.SetCallDepth(config.LogDepth)

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
	// if request is for the API then process it as an api request
	if strings.HasPrefix(req.URL.Path, "/api") {
		switch strings.Split("/", req.URL.Path)[3] {
		case "ws":
			initWebSocket(resp, req)
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
