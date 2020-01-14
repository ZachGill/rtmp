package session

import (
	"bufio"
	"fmt"
	"github.com/torresjeff/rtmp-server/config"
	"github.com/torresjeff/rtmp-server/utils"
	"io"
	"net"
	"strings"
)

const RtmpVersion3 = 0x03


type surroundSound struct {
	stereoSound bool
	twoPointOneSound bool
	threePointOneSound bool
	fourPointZeroSound bool
	fourPointOneSound bool
	fivePointOneSound bool
	sevenPointOneSound bool
}

type clientMetadata struct {
	duration float64
	fileSize float64
	width           float64
	height          float64
	videoCodecID    string
	videoDataRate   float64
	frameRate       float64
	audioCodecID    string
	// number representation of audioCodecID (ffmpeg sends audioCodecID as a number rather than a string (like obs))
	iAudioCodecID    float64
	audioDataRate   float64
	audioSampleRate float64
	audioSampleSize float64
	audioChannels   float64
	sound           surroundSound
	encoder         string
}

// Represents a connection made with the RTMP server where messages are exchanged between client/server.
type Session struct {
	id             uint32
	conn           net.Conn
	socket         *bufio.ReadWriter
	clientMetadata clientMetadata

	//handshakeState HandshakeState
	c1 []byte
	s1 []byte

	// Interprets messages, calling the appropriate callback on the session
	messageManager *MessageManager

	// app data
	app                string
	flashVer           string
	swfUrl             string
	tcUrl              string
	typeOfStream       string
	streamKey          string // used to identify user
}

type WindowAckSizeCallback func(uint32)

func NewSession(conn *net.Conn) *Session {
	session := &Session{
		id:             GenerateSessionId(),
		conn:           *conn,
		socket:         bufio.NewReadWriter(bufio.NewReaderSize(*conn, config.BuffioSize), bufio.NewWriterSize(*conn, config.BuffioSize)),
	}
	chunkHandler := NewChunkHandler(session.socket)
	session.messageManager = NewMessageManager(session, chunkHandler)
	RegisterSession(session.id, session)
	return session
}

// Run performs the initial handshake and starts receiving streams of data.
func (session *Session) Run() error {
	// Perform handshake
	err := session.Handshake()
	if err != nil {
		session.conn.Close()
		return err
	}

	if config.Debug {
		fmt.Println("Handshake completed successfully")
	}

	// After handshake, start reading chunks
	for {
		if err := session.messageManager.nextMessage(); err == io.EOF {
			session.conn.Close()
			return nil
		} else if err != nil {
			return err
		}
	}
}

func (session *Session) Handshake() error {
	var err error
	if err = session.readC0C1(); err != nil {
		return err
	}
	if err = session.sendS0S1S2(); err != nil {
		return err
	}
	if err = session.readC2(); err != nil {
		return err
	}

	return nil
}

func (session *Session) GetID() uint32 {
	return session.id
}

func (session *Session) send(bytes []byte) error {
	if _, err := session.socket.Write(bytes); err != nil {
		return err
	}
	if err := session.socket.Flush(); err != nil {
		return err
	}
	return nil
}

func (session *Session) write(bytes []byte) error {
	if _, err := session.socket.Write(bytes); err != nil {
		return err
	}
	return nil
}

func (session *Session) flush() error {
	if err := session.socket.Flush(); err != nil {
		return err
	}
	return nil
}

func (session *Session) read(bytes []byte) error {
	if _, err := session.socket.Read(bytes); err != nil {
		return err
	}
	return nil
}

func (session *Session) readC0C1() error {
	var c0c1 [1537]byte
	err := session.read(c0c1[:])
	if err != nil {
		return err
	}
	// Extract c1 message (which starts at byte 1) and store it for future use (sending it in S2)
	session.c1 = c0c1[1:]
	return nil
}

func (session *Session) readC2() error {
	var c2 [1536]byte
	if err := session.read(c2[:]); err != nil {
		return err
	}
	return nil
}

// Sends the s0, s1, and s2 sequence
func (session *Session) sendS0S1S2() error {
	var s0s1s2 [1 + 2*1536]byte
	var err error
	// s0 message is stored in byte 0
	s0s1s2[0] = RtmpVersion3
	// s1 message is stored in bytes 1-1536
	if err = session.generateS1(s0s1s2[1:1537]); err != nil {
		return err
	}
	// s2 message is stored in bytes 1537-3073
	if err = session.generateS2(s0s1s2[1537:]); err != nil {
		return err
	}

	return session.send(s0s1s2[:])
}

// Generates an S1 message and stores it in s1
func (session *Session) generateS1(s1 []byte) error {
	// the s1 byte array is zero-initialized, since we didn't modify it, we're sending our time as 0
	err := utils.GenerateRandomDataFromBuffer(s1[8:])
	if err != nil {
		return err
	}
	session.s1 = s1[:]
	return nil
}


// Generates an S1 message and stores it in s2. The S2 message is an echo of the C1 message
func (session *Session) generateS2(s2 []byte) error {
	copy(s2[:], session.c1)
	return nil
}

func (session *Session) onWindowAckSizeReached(sequenceNumber uint32) {
}

func (session *Session) onConnect(csID uint32, transactionID float64, commandObject map[string]interface{}) {
	session.storeMetadata(commandObject)
	// If the app name to connect  is PublishApp (whatever the user specifies in the config, ie. "app", "app/publish"),
	// this means the user wants to stream, follow the flow to start a stream
	if session.app == config.PublishApp {
		// Initiate connect sequence
		// As per the specification, after the connect command, the server sends the protocol message Window Acknowledgment Size
		session.messageManager.sendWindowAckSize(config.DefaultClientWindowSize)
		// TODO: connect to the app (whatever that is)
		// After sending the window ack size message, the server sends the set peer bandwidth message
		session.messageManager.sendSetPeerBandWidth(config.DefaultClientWindowSize, LimitDynamic)
		// Send the User Control Message to begin stream with stream ID = DefaultPublishStream (which is 0)
		// Subsequent messages sent by the client will have stream ID = DefaultPublishStream, until another sendBeginStream message is sent
		session.messageManager.sendBeginStream(config.DefaultPublishStream)
		// Send Set Chunk Size message
		session.messageManager.sendSetChunkSize(config.DefaultChunkSize)
		// Send Connect Success response
		session.messageManager.sendConnectSuccess(csID)
	} else {
		fmt.Println("session: user trying to connect to app", session.app, ", but the app doesn't exist")
	}
}

func (session *Session) storeMetadata(metadata map[string]interface{}) {
	// Playback clients send other properties in the command object, such as what audio/video codecs the client supports
	session.app = metadata["app"].(string)
	if _, exists := metadata["flashVer"]; exists {
		session.flashVer = metadata["flashVer"].(string)
	} else if _, exists := metadata["flashver"]; exists {
		session.flashVer = metadata["flashver"].(string)
	}

	if _, exists := metadata["swfUrl"]; exists {
		session.flashVer = metadata["swfUrl"].(string)
	} else if _, exists := metadata["swfurl"]; exists {
		session.flashVer = metadata["swfurl"].(string)
	}

	if _, exists := metadata["tcUrl"]; exists {
		session.flashVer = metadata["tcUrl"].(string)
	} else if _, exists := metadata["tcurl"]; exists {
		session.flashVer = metadata["tcurl"].(string)
	}

	if _, exists := metadata["type"]; exists {
		session.flashVer = metadata["type"].(string)
	}
}

func (session *Session) onSetChunkSize(size uint32) {
	session.messageManager.SetChunkSize(size)
}

func (session *Session) onAbortMessage(chunkStreamId uint32) {
}

func (session *Session) onAck(sequenceNumber uint32) {
}

func (session *Session) onSetWindowAckSize(windowAckSize uint32) {
	session.messageManager.SetWindowAckSize(windowAckSize)
}

func (session *Session) onSetBandwidth(windowAckSize uint32, limitType uint8) {
	session.messageManager.SetBandwidth(windowAckSize, limitType)
}

func (session *Session) onSetClientMetadata(metadata map[string]interface{}) {
	// Put all keys in lowercase to handle different formats for each client uniformly
	for key, val := range metadata {
		metadata[strings.ToLower(key)] = val
	}

	if val, exists := metadata["duration"]; exists {
		session.clientMetadata.duration = val.(float64)
	}
	if val, exists := metadata["filesize"]; exists {
		session.clientMetadata.fileSize = val.(float64)
	}
	if val, exists := metadata["width"]; exists {
		session.clientMetadata.width = val.(float64)
	}
	if val, exists := metadata["height"]; exists {
		session.clientMetadata.height = val.(float64)
	}
	if val, exists := metadata["videocodecid"]; exists {
		session.clientMetadata.videoCodecID = val.(string)
	}
	if val, exists := metadata["videodatarate"]; exists {
		session.clientMetadata.videoDataRate = val.(float64)
	}
	if val, exists := metadata["framerate"]; exists {
		session.clientMetadata.frameRate = val.(float64)
	}
	if val, exists := metadata["audiocodecid"]; exists {
		switch val.(type) {
		case float64:
			session.clientMetadata.iAudioCodecID = val.(float64)
		case string:
			session.clientMetadata.audioCodecID = val.(string)
		}
	}
	if val, exists := metadata["audiodatarate"]; exists {
		session.clientMetadata.audioDataRate = val.(float64)
	}
	if val, exists := metadata["audiosamplerate"]; exists {
		session.clientMetadata.audioSampleRate = val.(float64)
	}
	if val, exists := metadata["audiosamplesize"]; exists {
		session.clientMetadata.audioSampleSize = val.(float64)
	}
	if val, exists := metadata["audiochannels"]; exists {
		session.clientMetadata.audioChannels = val.(float64)
	}
	if val, exists := metadata["stereo"]; exists {
		session.clientMetadata.sound.stereoSound = val.(bool)
	}
	if val, exists := metadata["2.1"]; exists {
		session.clientMetadata.sound.twoPointOneSound = val.(bool)
	}
	if val, exists := metadata["3.1"]; exists {
		session.clientMetadata.sound.threePointOneSound = val.(bool)
	}
	if val, exists := metadata["4.0"]; exists {
		session.clientMetadata.sound.fourPointZeroSound = val.(bool)
	}
	if val, exists := metadata["4.1"]; exists {
		session.clientMetadata.sound.fourPointOneSound = val.(bool)
	}
	if val, exists := metadata["5.1"]; exists {
		session.clientMetadata.sound.fivePointOneSound = val.(bool)
	}
	if val, exists := metadata["7.1"]; exists {
		session.clientMetadata.sound.sevenPointOneSound = val.(bool)
	}
	if val, exists := metadata["encoder"]; exists {
		session.clientMetadata.encoder = val.(string)
	}

	if config.Debug {
		fmt.Printf("clientMetadata %+v", session.clientMetadata)
	}
}

func (session *Session) onReleaseStream(csID uint32, transactionID float64, streamKey string) {
	// TODO: what does releaseStream actually do? Does it close the stream?
	// TODO: check stuff like is stream key valid, is the user allowed to release the stream

	// TODO: implement
}

func (session *Session) onFCPublish(csID uint32, transactionID float64, streamKey string) {
	// TODO: check stuff like is stream key valid, is the user allowed to publish to the stream

	session.sendOnFCPublish(csID, transactionID, streamKey)
}

func (session *Session) sendOnFCPublish(csID uint32, transactionID float64, streamKey string) {
	message := generateOnFCPublishMessage(csID, transactionID, streamKey)
	session.socket.Write(message)
	session.socket.Flush()
}

func (session *Session) onCreateStream(csID uint32, transactionID float64, commandObject map[string]interface{}) {
	message := generateCreateStreamResponse(csID, transactionID, commandObject)
	session.socket.Write(message)
	session.socket.Flush()

	session.messageManager.sendBeginStream(uint32(config.DefaultStreamID))
}

func (session *Session) onPublish(csID uint32, streamID uint32, transactionID float64, streamKey string, publishingType string) {
	// TODO: Handle things like look up the user's stream key, check if it's valid.
	// TODO: For example: twitch returns "Publishing live_user_<username>" in the description.
	// TODO: Handle things like recording into a file if publishingType = "record" or "append"
	// infoObject should have at least three properties: level, code, and description. But may contain other properties.
	infoObject := map[string]interface{}{
		"level": "status",
		"code": "NetStream.Publish.Start",
		"description": "Publishing live_user_<x>",
	}
	// TODO: the transaction ID for onStatus messages should be 0 as per the spec. But twitch sends the transaction ID that was in the request to "publish".
	// For now, reply with the same transaction ID.
	message := generateStatusMessage(transactionID, streamID, infoObject)
	session.socket.Write(message)
	session.socket.Flush()
}