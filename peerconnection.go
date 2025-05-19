package main

import (
	"log/slog"

	"github.com/3DRX/fec-test/interceptor/flexfec"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/nack"
	"github.com/pion/interceptor/pkg/twcc"
	"github.com/pion/sdp/v3"

	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/codec/openh264"
	_ "github.com/pion/mediadevices/pkg/driver/camera"
	"github.com/pion/mediadevices/pkg/prop"
	"github.com/pion/webrtc/v4"
)

type PeerConnectionThread struct {
	sendSDPChan       chan<- webrtc.SessionDescription
	recvSDPChan       <-chan webrtc.SessionDescription
	sendCandidateChan chan<- webrtc.ICECandidateInit
	peerConnection    *webrtc.PeerConnection
}

func NewPeerConnectionThread(
	sendSDPChan chan<- webrtc.SessionDescription,
	recvSDPChan <-chan webrtc.SessionDescription,
	sendCandidateChan chan<- webrtc.ICECandidateInit,
) *PeerConnectionThread {
	m := &webrtc.MediaEngine{}
	i := &interceptor.Registry{}
	codecselector, err := configureCodec(m)
	if err != nil {
		panic(err)
	}
	fecInterceptor, err := flexfec.NewFecInterceptor()
	if err != nil {
		panic(err)
	}
	twccInterceptor, err := twcc.NewHeaderExtensionInterceptor()
	if err != nil {
		panic(err)
	}
	nackResponder, err := nack.NewResponderInterceptor()
	if err != nil {
		panic(err)
	}
	i.Add(twccInterceptor)
	i.Add(fecInterceptor)
	i.Add(nackResponder)
	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(m),
		webrtc.WithInterceptorRegistry(i),
	)
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}
	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}
	slog.Info("Created peer connection")

	mediaStream, err := mediadevices.GetUserMedia(mediadevices.MediaStreamConstraints{
		Video: func(constraint *mediadevices.MediaTrackConstraints) {
			constraint.Width = prop.Int(1280)
			constraint.Height = prop.Int(720)
			constraint.FrameRate = prop.Float(30)
		},
		Codec: codecselector,
	})
	if err != nil {
		panic(err)
	}
	for _, videoTrack := range mediaStream.GetVideoTracks() {
		videoTrack.OnEnded(func(err error) {
			slog.Error("Track ended", "error", err)
		})
		t, err := peerConnection.AddTransceiverFromTrack(
			videoTrack,
			webrtc.RTPTransceiverInit{
				Direction: webrtc.RTPTransceiverDirectionSendonly,
			},
		)
		if err != nil {
			panic(err)
		}
		slog.Info("add video track success", "encodings", t.Sender().GetParameters().Encodings)
	}

	pc := &PeerConnectionThread{
		sendSDPChan:       sendSDPChan,
		recvSDPChan:       recvSDPChan,
		sendCandidateChan: sendCandidateChan,
		peerConnection:    peerConnection,
	}
	return pc
}

func (pc *PeerConnectionThread) Spin() {
	datachannel, err := pc.peerConnection.CreateDataChannel("controller", nil)
	if err != nil {
		panic(err)
	}
	datachannel.OnOpen(func() {
		slog.Info("datachannel open", "label", datachannel.Label(), "ID", datachannel.ID())
	})

	offer, err := pc.peerConnection.CreateOffer(nil)
	if err != nil {
		panic(err)
	}
	pc.peerConnection.SetLocalDescription(offer)
	pc.peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		pc.sendCandidateChan <- c.ToJSON()
	})
	pc.sendSDPChan <- offer
	remoteSDP := <-pc.recvSDPChan
	slog.Info("Before calling SetRemoteDescription", "sender parameters", pc.peerConnection.GetTransceivers()[0].Sender().GetParameters())
	pc.peerConnection.SetRemoteDescription(remoteSDP)

	select {}
}

func configureCodec(m *webrtc.MediaEngine) (*mediadevices.CodecSelector, error) {
	if err := m.RegisterHeaderExtension(
		webrtc.RTPHeaderExtensionCapability{URI: sdp.TransportCCURI}, webrtc.RTPCodecTypeVideo,
	); err != nil {
		return nil, err
	}
	params, err := openh264.NewParams()
	if err != nil {
		return nil, err
	}
	params.BitRate = 2_000_000 // 2Mbps
	codecselector := mediadevices.NewCodecSelector(mediadevices.WithVideoEncoders(&params))
	codecselector.Populate(m)
	err = m.RegisterCodec(
		webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeRTX,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "apt=125",
				RTCPFeedback: nil,
			},
			PayloadType: 126,
		},
		webrtc.RTPCodecTypeVideo,
	)
	if err != nil {
		return nil, err
	}
	m.RegisterFeedback(webrtc.RTCPFeedback{Type: "nack"}, webrtc.RTPCodecTypeVideo)
	m.RegisterFeedback(webrtc.RTCPFeedback{Type: "nack", Parameter: "pli"}, webrtc.RTPCodecTypeVideo)
	err = m.RegisterCodec(
		webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeFlexFEC03,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "repair-window=10000000",
				RTCPFeedback: nil,
			},
			PayloadType: 118,
		},
		webrtc.RTPCodecTypeVideo,
	)
	if err != nil {
		return nil, err
	}
	return codecselector, nil
}
