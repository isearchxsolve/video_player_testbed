package main

import (
	"testing"
	"time"
)

func TestNewAVSynchronizerRejectsInvalidSampleRate(t *testing.T) {
	if _, err := NewAVSynchronizer(0); err != ErrInvalidSampleRate {
		t.Fatalf("expected ErrInvalidSampleRate, got %v", err)
	}
}

func TestAudioClockAdvancesFromRenderedAudioOnly(t *testing.T) {
	sync, err := NewAVSynchronizer(48000)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := sync.Clock(); ok {
		t.Fatal("clock must not be valid before Reset")
	}

	sync.Reset(2 * time.Second)
	if err := sync.AudioRendered(24000); err != nil {
		t.Fatal(err)
	}

	got, ok := sync.Clock()
	if !ok {
		t.Fatal("clock should be valid after Reset")
	}
	want := 2500 * time.Millisecond
	if got != want {
		t.Fatalf("clock = %v, want %v", got, want)
	}
}

func TestAudioClockAppliesPlaybackRate(t *testing.T) {
	sync, err := NewAVSynchronizer(48000)
	if err != nil {
		t.Fatal(err)
	}

	sync.Reset(0)
	if err := sync.SetRate(1.5); err != nil {
		t.Fatal(err)
	}
	if err := sync.AudioRendered(48000); err != nil {
		t.Fatal(err)
	}

	got, _ := sync.Clock()
	if got != 1500*time.Millisecond {
		t.Fatalf("clock = %v, want 1.5s", got)
	}
}

func TestSetRateRejectsUnsafeRates(t *testing.T) {
	sync, _ := NewAVSynchronizer(48000)

	for _, rate := range []float64{0, 0.24, 4.01} {
		if err := sync.SetRate(rate); err != ErrInvalidRate {
			t.Fatalf("rate %v: expected ErrInvalidRate, got %v", rate, err)
		}
	}
}

func TestDecideWaitsForEarlyVideo(t *testing.T) {
	sync, _ := NewAVSynchronizer(48000)
	sync.Reset(0)

	decision := sync.Decide(80 * time.Millisecond)
	if decision.Action != FrameWait {
		t.Fatalf("action = %v, want FrameWait", decision.Action)
	}
	if decision.Delay != 80*time.Millisecond {
		t.Fatalf("delay = %v, want 80ms", decision.Delay)
	}
}

func TestDecidePresentsNearAudioClock(t *testing.T) {
	sync, _ := NewAVSynchronizer(48000)
	sync.Reset(0)
	if err := sync.AudioRendered(48000); err != nil {
		t.Fatal(err)
	}

	decision := sync.Decide(1010 * time.Millisecond)
	if decision.Action != FramePresent {
		t.Fatalf("action = %v, want FramePresent", decision.Action)
	}
}

func TestDecideDropsStaleFrames(t *testing.T) {
	sync, _ := NewAVSynchronizer(48000)
	sync.Reset(0)
	if err := sync.AudioRendered(48000); err != nil {
		t.Fatal(err)
	}

	decision := sync.Decide(800 * time.Millisecond)
	if decision.Action != FrameDrop {
		t.Fatalf("action = %v, want FrameDrop", decision.Action)
	}
}

func TestSeekResetRejectsOldTimeline(t *testing.T) {
	sync, _ := NewAVSynchronizer(48000)
	sync.Reset(0)
	if err := sync.AudioRendered(48000 * 10); err != nil {
		t.Fatal(err)
	}

	sync.Reset(30 * time.Second)
	clock, ok := sync.Clock()
	if !ok || clock != 30*time.Second {
		t.Fatalf("clock after seek = %v, %v; want 30s, true", clock, ok)
	}

	decision := sync.Decide(10 * time.Second)
	if decision.Action != FrameDrop {
		t.Fatalf("old frame action = %v, want FrameDrop", decision.Action)
	}
}

func TestAudioRenderedRejectsNegativeSamples(t *testing.T) {
	sync, _ := NewAVSynchronizer(48000)
	sync.Reset(0)

	if err := sync.AudioRendered(-1); err != ErrInvalidSamples {
		t.Fatalf("expected ErrInvalidSamples, got %v", err)
	}
}
