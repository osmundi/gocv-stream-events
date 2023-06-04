package main

import "strings"

//go:generate go run golang.org/x/tools/cmd/stringer -type=deviceSource
type deviceSource int

const (
	IMAGE deviceSource = iota
	VIDEO
	STREAM
)

type detectedObject struct {
	confidence               float32
	top, left, width, height int
	label                    string
}

func getDeviceType(deviceID string) deviceSource {
	if strings.HasSuffix(deviceID, ".jpg") || strings.HasSuffix(deviceID, ".png") {
		return IMAGE
	} else if strings.HasSuffix(deviceID, ".mp4") || deviceID == "0" {
		return VIDEO
	} else if strings.HasPrefix(deviceID, "rtsp") {
		return STREAM
	}
	return -1
}
