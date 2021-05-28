module github.com/gidoBOSSftw5731/ProjectEdison/server

go 1.13

require (
	barista.run v0.0.0-20210521202553-e76ea38ff543
	github.com/gidoBOSSftw5731/log v0.0.0-20210527210830-1611311b4b64
	github.com/gorilla/websocket v1.4.2
	github.com/jinzhu/configor v1.2.1
	github.com/prometheus/common v0.25.0
)

replace github.com/gidoBOSSftw5731/ProjectEdison/edison_proto => ./proto
