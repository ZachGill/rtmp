package main

import (
	"fmt"
	"github.com/ZachGill/rtmp"
	"github.com/ZachGill/rtmp/audio"
	"log"
)

func OnAudio(format audio.Format, sampleRate audio.SampleRate, sampleSize audio.SampleSize, channels audio.Channel, payload []byte, timestamp uint32) {
	fmt.Println("client: on audio")
}

func OnMetadata(metadata map[string]interface{}) {
	fmt.Printf("client: on metadata: %+v", metadata)
}

func main() {
	// Specify audio, video and metadata callbacks
	client := &rtmp.Client{
		OnAudio:    OnAudio,
		OnMetadata: OnMetadata,
	}

	err := client.Connect("rtmp://localhost/app/obs")
	if err != nil {
		log.Fatal(err)
	}
	//log.Fatal(client.Connect("rtmp://live-atl.twitch.tv/app/stremKey"))
}
