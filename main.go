package main

import "github.com/pion/webrtc/v4"

func main() {
	sendSDPChan := make(chan webrtc.SessionDescription)
	recvSDPChan := make(chan webrtc.SessionDescription)
	sendCandidateChan := make(chan webrtc.ICECandidateInit)

	signalingThread := NewSignalingThread(
		sendSDPChan,
		recvSDPChan,
		sendCandidateChan,
	)
	haveReceiverPromise := signalingThread.Spin()
	<-haveReceiverPromise
	peerConnectionThread := NewPeerConnectionThread(
		sendSDPChan,
		recvSDPChan,
		sendCandidateChan,
	)
	peerConnectionThread.Spin()
}
