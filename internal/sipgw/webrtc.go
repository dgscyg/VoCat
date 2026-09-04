package sipgw

import (
	"context"
	"io"
	"time"

	"github.com/pion/webrtc/v4"
	pionmedia "github.com/pion/webrtc/v4/pkg/media"

	"vocat/internal/vowifi"
)

func newPeerConnection() (*webrtc.PeerConnection, error) {
	engine := &webrtc.MediaEngine{}
	if err := engine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypePCMU, ClockRate: 8000, Channels: 1, SDPFmtpLine: "",
		},
		PayloadType: 0,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(engine))
	return api.NewPeerConnection(webrtc.Configuration{})
}

func answerWebRTC(offerSDP string) (*webrtc.PeerConnection, *webrtc.TrackLocalStaticSample, string, error) {
	pc, err := newPeerConnection()
	if err != nil {
		return nil, nil, "", err
	}
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMU, ClockRate: 8000, Channels: 1}, "audio", "vocat")
	if err != nil {
		_ = pc.Close()
		return nil, nil, "", err
	}
	if _, err := pc.AddTrack(track); err != nil {
		_ = pc.Close()
		return nil, nil, "", err
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP}); err != nil {
		_ = pc.Close()
		return nil, nil, "", err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = pc.Close()
		return nil, nil, "", err
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		_ = pc.Close()
		return nil, nil, "", err
	}
	select {
	case <-gather:
	case <-time.After(3 * time.Second):
	}
	local := pc.LocalDescription()
	if local == nil {
		_ = pc.Close()
		return nil, nil, "", io.ErrUnexpectedEOF
	}
	return pc, track, local.SDP, nil
}

func offerWebRTC() (*webrtc.PeerConnection, *webrtc.TrackLocalStaticSample, string, error) {
	pc, err := newPeerConnection()
	if err != nil {
		return nil, nil, "", err
	}
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMU, ClockRate: 8000, Channels: 1}, "audio", "vocat")
	if err != nil {
		_ = pc.Close()
		return nil, nil, "", err
	}
	if _, err := pc.AddTrack(track); err != nil {
		_ = pc.Close()
		return nil, nil, "", err
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		_ = pc.Close()
		return nil, nil, "", err
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		_ = pc.Close()
		return nil, nil, "", err
	}
	select {
	case <-gather:
	case <-time.After(3 * time.Second):
	}
	local := pc.LocalDescription()
	if local == nil {
		_ = pc.Close()
		return nil, nil, "", io.ErrUnexpectedEOF
	}
	return pc, track, local.SDP, nil
}

func bridgeMedia(ctx context.Context, pc *webrtc.PeerConnection, track *webrtc.TrackLocalStaticSample, media vowifi.CallMedia) {
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		go func() {
			for {
				packet, _, err := remote.ReadRTP()
				if err != nil {
					return
				}
				if ctx.Err() != nil {
					return
				}
				_ = media.WritePCM(pcmuToPCM(packet.Payload))
			}
		}()
	})
	go func() {
		for {
			samples, err := media.ReadPCM(ctx)
			if err != nil {
				return
			}
			_ = track.WriteSample(pionmedia.Sample{Data: pcmToPCMU(samples), Duration: 20 * time.Millisecond})
		}
	}()
}
