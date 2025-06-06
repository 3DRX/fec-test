// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package flexfec

import (
	"log/slog"
	"sync"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
)

// FecInterceptor implements FlexFec.
type FecInterceptor struct {
	interceptor.NoOp
	flexFecEncoder     FlexEncoder
	packetBuffer       []rtp.Packet
	minNumMediaPackets uint32
	mu                 sync.Mutex
}

// FecOption can be used to set initial options on Fec encoder interceptors.
type FecOption func(d *FecInterceptor) error

// FecInterceptorFactory creates new FecInterceptors.
type FecInterceptorFactory struct {
	opts []FecOption
}

// NewFecInterceptor returns a new Fec interceptor factory.
func NewFecInterceptor(opts ...FecOption) (*FecInterceptorFactory, error) {
	return &FecInterceptorFactory{opts: opts}, nil
}

// NewInterceptor constructs a new FecInterceptor.
func (r *FecInterceptorFactory) NewInterceptor(_ string) (interceptor.Interceptor, error) {
	interceptor := &FecInterceptor{
		packetBuffer:       make([]rtp.Packet, 0),
		minNumMediaPackets: 5,
	}

	return interceptor, nil
}

// BindLocalStream lets you modify any outgoing RTP packets. It is called once for per LocalStream. The returned method
// will be called once per rtp packet.
func (r *FecInterceptor) BindLocalStream(
	info *interceptor.StreamInfo, writer interceptor.RTPWriter,
) interceptor.RTPWriter {
	slog.Info(
		"FecInterceptor BindLocalStream",
		"ssrc",
		info.SSRC,
		"fec",
		info.SSRCForwardErrorCorrection,
		"mimeType",
		info.MimeType,
		"payloadType",
		info.PayloadType,
		"payloadTypeFec",
		info.PayloadTypeForwardErrorCorrection,
	)
	mediaSSRC := info.SSRC
	// Chromium supports version flexfec-03 of existing draft, this is the one we will configure by default
	// although we should support configuring the latest (flexfec-20) as well.
	r.flexFecEncoder = NewFlexEncoder03(info.PayloadTypeForwardErrorCorrection, info.SSRCForwardErrorCorrection)

	return interceptor.RTPWriterFunc(
		func(header *rtp.Header, payload []byte, attributes interceptor.Attributes) (int, error) {
			// ignore none media packets
			if header.SSRC != mediaSSRC {
				result, err := writer.Write(header, payload, attributes)
				return result, err
			}
			r.packetBuffer = append(r.packetBuffer, rtp.Packet{
				Header:  *header,
				Payload: payload,
			})
			// Send the media RTP packet
			result, err := writer.Write(header, payload, attributes)

			// Send the FEC packets
			if len(r.packetBuffer) == int(r.minNumMediaPackets) {
				fecPackets := r.flexFecEncoder.EncodeFec(r.packetBuffer, 2)

				for i := range fecPackets {
					fecResult, fecErr := writer.Write(&(fecPackets[i].Header), fecPackets[i].Payload, attributes)

					if fecErr != nil && fecResult == 0 {
						break
					}
				}
				// Reset the packet buffer now that we've sent the corresponding FEC packets.
				r.packetBuffer = nil
			}

			return result, err
		},
	)
}
