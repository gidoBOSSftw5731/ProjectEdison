package main

import (
	"fmt"
	"net/http"
	"path"
	"strings"
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
	sockets     []*websocket.Conn
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
		obdConn, err = elmobd.NewDevice(config.OBD2Path, false)
	case true:
		obdConn, err = elmobd.NewTestDevice(config.OBD2Path, true)
	}
	if err != nil {
		log.Errorln("Error connecting to OBD2: ", err)
	}

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
		//elmobd.NewEngineLoad(),
		elmobd.NewFuel(),
		elmobd.NewEngineRPM(),
		//also not supported
		//elmobd.NewFuelPressure(),
		elmobd.NewVehicleSpeed(),
		elmobd.NewVehicleSpeed(),
		//not supported
		//elmobd.NewIntakeAirTemperature(),
	)
	if err != nil {
		return &p, err
	}

	return &pb.CarStatus{
		FuelLevel:   commands[0].(*elmobd.Fuel).FloatCommand.Value,
		CoolantTemp: int32(commands[1].(*elmobd.CoolantTemperature).IntCommand.Value),
		//EngineLoad:    commands[2].(*elmobd.EngineLoad).FloatCommand.Value,
		EngineRPM: commands[3].(*elmobd.EngineRPM).FloatCommand.Value,
		//FuelPressure:  commands[4].(*elmobd.FuelPressure).UIntCommand.Value,
		VehicleSpeed: commands[5].(*elmobd.VehicleSpeed).UIntCommand.Value,
		//IntakeAirTemp: int32(commands[6].(*elmobd.IntakeAirTemperature).IntCommand.Value),
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
		case "musicconnected":
			p := musicDataToProto()
			buf, err := prototext.Marshal(&p)
			if err != nil {
				log.Errorln(err)
				return
			}
			fmt.Fprintf(resp, "%s", buf)
		case "obdtest":
			p, err := obdDataToProto()
			if err != nil {
				log.Errorln("Error getting obd data: ", err)
				return
			}

			buf, err := prototext.Marshal(p)
			if err != nil {
				log.Errorln("Error marshalling obd data: ", err)
				return
			}

			log.Tracef("%s", buf)
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

	sockets = append(sockets, conn)

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

// throwaway sink because barista is meant for i3 bars and all I want is to cannibalize
// it for its mpris functionality
func sink(o bar.Output) {
	// log.Tracef("Sink output: %#v", o)
}

func musicData(info barista.Info) bar.Output {
	musicInfo = info
	return nil
}

func musicDataToProto() pb.MusicStatus {
	return pb.MusicStatus{
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
