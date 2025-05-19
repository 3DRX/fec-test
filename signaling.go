package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/3DRX/vaporplay/config"
	"github.com/3DRX/vaporplay/middleware"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

type SignalingThread struct {
	upgrader            *websocket.Upgrader
	conn                *websocket.Conn
	haveReceiverPromise chan struct{}
	sendSDPChan         <-chan webrtc.SessionDescription
	recvSDPChan         chan<- webrtc.SessionDescription
	sendCandidateChan   <-chan webrtc.ICECandidateInit
	connecting          bool
	httpServer          *http.Server
}

func NewSignalingThread(
	sendSDPChan <-chan webrtc.SessionDescription,
	recvSDPChan chan<- webrtc.SessionDescription,
	sendCandidateChan <-chan webrtc.ICECandidateInit,
) *SignalingThread {
	return &SignalingThread{
		upgrader: &websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		conn:                nil,
		haveReceiverPromise: make(chan struct{}),
		sendSDPChan:         sendSDPChan,
		recvSDPChan:         recvSDPChan,
		sendCandidateChan:   sendCandidateChan,
		connecting:          false,
	}
}

func (s *SignalingThread) Spin() <-chan struct{} {
	mux := http.NewServeMux()
	mux.Handle("GET /games", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		games := []config.GameConfig{{
			GameId:          "000000",
			GameWindowName: "system desktop",
			GameDisplayName: "system desktop",
			EndGameCommands: []config.KillProcessCommandConfig{},
		}}
		jsonGames, err := json.Marshal(games)
		if err != nil {
			slog.Error("failed to marshal games", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write(jsonGames)
		return
	}))
	mux.Handle("GET /webrtc", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.conn != nil {
			slog.Warn("already have a receiver, rejecting new connection")
			w.WriteHeader(http.StatusConflict)
			return
		}
		conn, err := s.upgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Error("failed to upgrade connection", "error", err)
			return
		}
		slog.Info("new receiver connected")
		s.conn = conn
		go s.handleRecvMessages()
		go s.handleSendMessages()
	}))

	httpServer := &http.Server{
		Addr: "0.0.0.0:8080",
		Handler: middleware.ChainMiddleware(
			mux,
			middleware.CORSMiddleware,
		),
	}
	s.httpServer = httpServer
	go func() {
		err := httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}()
	return s.haveReceiverPromise
}

func (s *SignalingThread) Close() error {
	if s.httpServer != nil {
		if s.conn != nil {
			if err := s.conn.Close(); err != nil {
				return err
			}
		}
		return s.httpServer.Shutdown(context.Background())
	}
	return nil
}

func (s *SignalingThread) handleSendMessages() {
	for {
		select {
		case sdp := <-s.sendSDPChan:
			jsonMsg, err := json.Marshal(sdp)
			if err != nil {
				slog.Error("failed to marshal SDP", "error", err)
			}
			err = s.conn.WriteMessage(websocket.TextMessage, jsonMsg)
			if err != nil {
				slog.Error("websocket write error", "error", err)
			}
			slog.Info("sent SDP", "sdp", sdp.SDP)
		case candidate := <-s.sendCandidateChan:
			jsonMsg, err := json.Marshal(candidate)
			if err != nil {
				slog.Error("failed to marshal ICE candidate", "error", err)
			}
			err = s.conn.WriteMessage(websocket.TextMessage, jsonMsg)
			if err != nil {
				slog.Error("websocket write error", "error", err)
			}
			slog.Info("sent ICE candidate", "candidate", candidate)
		}
	}
}

func (s *SignalingThread) handleRecvMessages() {
	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			slog.Error("websocket read error", "error", err)
			s.conn.Close()
			return
		}
		if !s.connecting {
			s.haveReceiverPromise <- struct{}{}
			_, message, err = s.conn.ReadMessage()
			if err != nil {
				slog.Error("websocket read error", "error", err)
				s.conn.Close()
				return
			}
			// try to parse it as an SDP
			newSDP := webrtc.SessionDescription{}
			err = json.Unmarshal(message, &newSDP)
			if err != nil {
				slog.Error("failed to parse message as SDP", "error", err)
				continue
			}
			slog.Info("received SDP", "sdp", newSDP.SDP)
			s.recvSDPChan <- newSDP
		}
	}
}
