package main

import (
	"errors"
	"math"
	"sync"
	"time"
)

var (
	ErrInvalidSampleRate = errors.New("audio sample rate must be positive")
	ErrInvalidRate       = errors.New("playback rate must be in range [0.25, 4.0]")
	ErrInvalidSamples    = errors.New("rendered audio sample count must not be negative")
)

// FrameAction specifies what the video render loop must do for a decoded frame.
type FrameAction uint8

const (
	FramePresent FrameAction = iota
	FrameWait
	FrameDrop
)

// FrameDecision is the synchronization result for one decoded video frame.
type FrameDecision struct {
	Action FrameAction
	Delay  time.Duration
}

// AVSynchronizer uses the audible audio stream as the authoritative media clock.
//
// Wire it as follows:
//
//   1. Call Reset(decodedAudioPTS) after seek, open, or stream discontinuity.
//   2. Call AudioRendered(samples) from the audio device callback only after the
//      samples have been accepted by the output device.
//   3. Call Decide(videoFramePTS) before presenting a decoded video frame.
//   4. When playback speed changes, reconfigure the FFmpeg audio resampler and
//      call SetRate with exactly the same rate.
type AVSynchronizer struct {
	mu sync.RWMutex

	sampleRate int
	rate       float64

	started       bool
	basePTS       time.Duration
	renderedAudio int64

	presentThreshold time.Duration
	dropThreshold    time.Duration
	maxWait          time.Duration
}

func NewAVSynchronizer(sampleRate int) (*AVSynchronizer, error) {
	if sampleRate <= 0 {
		return nil, ErrInvalidSampleRate
	}

	return &AVSynchronizer{
		sampleRate:       sampleRate,
		rate:             1.0,
		presentThreshold: 15 * time.Millisecond,
		dropThreshold:    100 * time.Millisecond,
		maxWait:          250 * time.Millisecond,
	}, nil
}

// Reset establishes a new audio timeline. It must be called immediately after
// avformat_seek_file/avcodec_flush_buffers and before resumed presentation.
func (s *AVSynchronizer) Reset(audioPTS time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.started = true
	s.basePTS = audioPTS
	s.renderedAudio = 0
}

// Stop invalidates the clock until the next Reset.
func (s *AVSynchronizer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.started = false
	s.renderedAudio = 0
}

// SetRate changes the media-time progression rate. The caller must configure
// FFmpeg's audio pipeline to emit time-stretched samples at this same rate.
func (s *AVSynchronizer) SetRate(rate float64) error {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0.25 || rate > 4.0 {
		return ErrInvalidRate
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.rate = rate
	return nil
}

// AudioRendered advances the media clock by output audio that has actually
// reached the platform audio device callback.
func (s *AVSynchronizer) AudioRendered(samples int) error {
	if samples < 0 {
		return ErrInvalidSamples
	}
	if samples == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return nil
	}

	const maxInt64 = int64(^uint64(0) >> 1)
	if int64(samples) > maxInt64-s.renderedAudio {
		s.renderedAudio = maxInt64
		return nil
	}

	s.renderedAudio += int64(samples)
	return nil
}

// Clock returns the current media PTS. The boolean is false before Reset.
func (s *AVSynchronizer) Clock() (time.Duration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.started {
		return 0, false
	}

	seconds := (float64(s.renderedAudio) / float64(s.sampleRate)) * s.rate
	elapsed := time.Duration(seconds * float64(time.Second))
	return s.basePTS + elapsed, true
}

// Decide returns the required presentation action for one video frame PTS.
func (s *AVSynchronizer) Decide(videoPTS time.Duration) FrameDecision {
	clock, ok := s.Clock()
	if !ok {
		// Audio initialization has not completed; never present against an
		// invented wall clock.
		return FrameDecision{Action: FrameWait, Delay: 5 * time.Millisecond}
	}

	s.mu.RLock()
	presentThreshold := s.presentThreshold
	dropThreshold := s.dropThreshold
	maxWait := s.maxWait
	s.mu.RUnlock()

	delta := videoPTS - clock

	if delta < -dropThreshold {
		return FrameDecision{Action: FrameDrop}
	}
	if delta <= presentThreshold {
		return FrameDecision{Action: FramePresent}
	}
	if delta > maxWait {
		delta = maxWait
	}

	return FrameDecision{Action: FrameWait, Delay: delta}
}
