package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rzetterberg/elmobd"

	"barista.run/bar"
	barista "barista.run/modules/media"
	"github.com/blackjack/webcam"
	pb "github.com/gidoBOSSftw5731/ProjectEdison/server/edison_proto"
	"github.com/gidoBOSSftw5731/log"
	"github.com/gorilla/websocket"
	"github.com/jinzhu/configor"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

var config = struct {
	// LogDepth defines the verbosity of the logs. The log package
	// details the appropriate values
	LogDepth int `default:"4"`

	// ListenAddr is the address that will be listened for HTTP requests. It defaults to
	// 127.0.0.1:8080
	ListenAddr string `default:"0.0.0.0:8080"`

	// CameraPath is the linux path to a video device, like a webcam or capture card
	// defaults to the first webcam (assuming v4l2 is installed and configured)
	CameraPath string `default:"/dev/video0"`

	PanicWithoutCamera bool `default:"false"`

	// This program will have rudementary OBD2 support and therefore will connect to one over
	// serial and/or bluetooth. This default is for bluetooth, rfcomm needs to be configured
	// elsewhere
	OBD2Path string `default:"/dev/rfcomm0"`

	// set to true to use a spoofed obd2 device
	Testing bool `default:"false"`
}{}

var (
	upgrader    = websocket.Upgrader{} // use default options
	musicPlayer *barista.AutoModule
	musicInfo   barista.Info
	sockets     = make(map[*websocket.Conn]bool)
	obdConn     *elmobd.Device
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

	// start webcam capture and stream in its own thread
	go webcamHandler()

	//connect to obd2
	switch config.Testing {
	case false:
		obdConn, err = elmobd.NewDevice(config.OBD2Path, false, false)
	case true:
		obdConn, err = elmobd.NewTestDevice(config.OBD2Path, true)
	}
	if err != nil {
		log.Errorln("Error connecting to OBD2: ", err)
	}

	//start websocket looper
	go wsBroadcaster()

	startHTTPListener()
}

func webcamHandler() {
	cam, err := webcam.Open(config.CameraPath)
	if err != nil {
		if config.PanicWithoutCamera {
			log.Panicln("Camera not found! Panicking as per conf: ", err)
		}
		log.Errorln("Error opening camera, not panicking as per config: ", err)
		return
	}
	defer cam.Close()

	// select pixel format
	format_desc := cam.GetSupportedFormats()

	log.Traceln("Available Camera formats:")
	for _, s := range format_desc {
		log.Traceln(s)
	}

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

func obdDataToProto() (*pb.CarStatus, error) {
	var p pb.CarStatus

	commands, err := obdConn.RunManyOBDCommands(
		elmobd.NewFuel(),
		elmobd.NewCoolantTemperature(),
		//engine load not supported by test, using random filler
		elmobd.NewEngineLoad(),
		//		elmobd.NewFuel(),
		elmobd.NewEngineRPM(),
		//also not supported
		elmobd.NewFuelPressure(),
		//elmobd.NewVehicleSpeed(),
		elmobd.NewVehicleSpeed(),
		//not supported
		elmobd.NewIntakeAirTemperature(),
	)
	if err != nil {
		return &p, err
	}

	return &pb.CarStatus{
		FuelLevel:     commands[0].(*elmobd.Fuel).FloatCommand.Value,
		CoolantTemp:   int32(commands[1].(*elmobd.CoolantTemperature).IntCommand.Value),
		EngineLoad:    commands[2].(*elmobd.EngineLoad).FloatCommand.Value,
		EngineRPM:     commands[3].(*elmobd.EngineRPM).FloatCommand.Value,
		FuelPressure:  commands[4].(*elmobd.FuelPressure).UIntCommand.Value,
		VehicleSpeed:  commands[5].(*elmobd.VehicleSpeed).UIntCommand.Value,
		IntakeAirTemp: int32(commands[6].(*elmobd.IntakeAirTemperature).IntCommand.Value),
	}, nil
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
		case "fullproto":
			p, err := makeFullProto()
			if err != nil {
				log.Errorln("Error getting full proto: ", err)
				return
			}

			buf, err := prototext.Marshal(p)
			if err != nil {
				log.Errorln("Error marshalling all data: ", err)
				return
			}
			log.Tracef("%s", buf)
			fmt.Fprintf(resp, "%s", buf)
		case "music":
			musicAPIHandler(resp, req)
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

// musicAPIHandler recieves requests relating to the music API and returns results.
func musicAPIHandler(resp http.ResponseWriter, req *http.Request) {
	urlPath := strings.Split(req.URL.Path, "/")

	if len(urlPath) <= 3 {
		http.Error(resp, "No argument supplied to music API", 400)
		return
	}

	switch urlPath[3] {
	case "play":
		musicInfo.Play()
	case "pause":
		musicInfo.Pause()
	case "playpause", "toggleplaying", "toggle":
		musicInfo.PlayPause()
	case "skip", "next":
		musicInfo.Next()
	case "previous", "back":
		musicInfo.Previous()
	case "stop":
		musicInfo.Stop()
	case "seek":
		if len(urlPath) != 5 {
			http.Error(resp, "Time in seconds required to seek", 400)
			return
		}

		seconds, err := strconv.Atoi(urlPath[4])
		if err != nil {
			http.Error(resp, "Error converting time in seconds to int", 500)
		}

		musicInfo.Seek(time.Duration(seconds) * time.Second)

	default:
		http.Error(resp, "Invalid argument supplied to music API", 400)
		return
	}
}

func initWebSocket(resp http.ResponseWriter, req *http.Request) {
	// Upgrade our raw HTTP connection to a websocket based one
	conn, err := upgrader.Upgrade(resp, req, nil)
	if err != nil {
		log.Errorln("Error during connection upgradation: ", err)
		return
	}
	defer conn.Close()

	//add to list of connections to broadcast to regularly and then remove it from list once
	// the conn is closed. Hypothetically, there's a small amount of time when both
	// the conn is closed and the socket is in the map, but this seems unlikely to cause issue
	sockets[conn] = true
	defer delete(sockets, conn)

EventLoop:
	for {
		//thread blocks here until message arrives
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

func wsBroadcaster() {
	done := make(chan interface{})         // Channel to indicate that the receiverHandler is done
	interrupt := make(chan os.Signal, 5)   // Channel to listen for interrupt signal to terminate gracefully
	signal.Notify(interrupt, os.Interrupt) // Notify the interrupt channel for SIGINT

	for {
		select {
		case <-time.After(time.Millisecond * 500):

			p, err := makeFullProto()
			if err != nil {
				log.Errorln("Error in making proto for wsloop: ", err)
				continue
			}
			buf, err := proto.Marshal(p)
			if err != nil {
				log.Errorln("Error marshalling proto in wsloop: ", err)
				continue
			}

			for conn := range sockets {
				err := conn.WriteMessage(websocket.BinaryMessage, buf)
				if err != nil {
					log.Println("Error during writing to websocket:", err)
					return
				}
			}

		case <-interrupt:
			// We received a SIGINT (Ctrl + C). Terminate gracefully...
			log.Infoln("Received SIGINT interrupt signal. Closing all pending connections")

			// create wg for making sure all conns close gracefully
			var wg sync.WaitGroup

			wg.Add(1)

			for conn := range sockets {
				wg.Add(1)
				go func() {
					defer wg.Done()
					err := conn.WriteMessage(websocket.CloseMessage,
						websocket.FormatCloseMessage(websocket.CloseNormalClosure,
							"Program Interrupted"))
					if err != nil {
						log.Errorln("Error during writing to websocket:", err)
						return
					}
				}()
			}
			wg.Done()

			go func() {
				wg.Wait()
				done <- true
			}()

			select {
			case <-done:
				log.Fatalln("Receiver Channel Closed! Exiting....")
				return
			case <-time.After(5 * time.Second):
				log.Fatalln("Timeout in closing receiving channel. Exiting....")
			}
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

func musicDataToProto() *pb.MusicStatus {
	return &pb.MusicStatus{
		PlayerName:     musicInfo.PlayerName,
		PlaybackStatus: string(musicInfo.PlaybackStatus),
		// convert Length from nanoseconds to milliseconds
		Length:      int32(musicInfo.Length / time.Millisecond),
		Title:       musicInfo.Title,
		Artist:      musicInfo.Artist,
		Album:       musicInfo.Album,
		AlbumArtist: musicInfo.AlbumArtist,
		Position:    int32(musicInfo.Position() / time.Millisecond),
	}
}

func makeFullProto() (*pb.Msg, error) {
	var p = pb.Msg{Music: musicDataToProto()}

	obdResp, err := obdDataToProto()
	if err != nil {
		return nil, err
	}

	p.Car = obdResp

	return &p, nil
}
