package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"math"
	"math/rand/v2"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/gopxl/beep/v2/wav"
	"github.com/taigrr/apple-silicon-accelerometer/detector"
	"github.com/taigrr/apple-silicon-accelerometer/sensor"
	"github.com/taigrr/apple-silicon-accelerometer/shm"
)

//go:embed sounds/*.wav
var soundFS embed.FS

const (
	sensorPollInterval = 10 * time.Millisecond

	// Thresholds adapted from MacSaber.m (originally for 8-bit values)
	// In the original:
	// hitThresh = 128 (out of ~255)
	// strikeThresh = 90
	// swingThresh = 20
	// Assuming that the original values were based on a range of 0-255,
	// we can convert them to g's (assuming 1g ~ 9.81 m/s² and that the original values were scaled accordingly).
	hitThreshold    = 0.6 // g
	strikeThreshold = 0.3 // g
	swingThreshold  = 0.1 // g
)

type SaberSound string

const (
	SoundHit    SaberSound = "hit"
	SoundIdle   SaberSound = "idle"
	SoundOff    SaberSound = "off"
	SoundOn     SaberSound = "on"
	SoundStart  SaberSound = "start"
	SoundStrike SaberSound = "strike"
	SoundSwing  SaberSound = "swing"
)

type Saber struct {
	buffers map[SaberSound][]*beep.Buffer
	format  beep.Format

	detector *detector.Detector

	// Movement state
	roll2, tilt2, rollD2, tiltD2 float64

	lastSwing time.Time
	lastHit   time.Time
}

func NewSaber() *Saber {
	return &Saber{
		buffers:  make(map[SaberSound][]*beep.Buffer),
		detector: detector.New(),
	}
}

func (s *Saber) LoadSounds(eff fs.FS, dir string) error {
	soundTypes := []SaberSound{SoundHit, SoundIdle, SoundOff, SoundOn, SoundStart, SoundStrike, SoundSwing}

	for _, st := range soundTypes {
		pattern := string(st) + "*.wav"
		matches, _ := fs.Glob(eff, filepath.Join(dir, pattern))
		for _, match := range matches {
			f, err := eff.Open(match)
			if err != nil {
				return err
			}
			streamer, format, err := wav.Decode(f)
			if err != nil {
				_ = f.Close()
				return err
			}

			if s.format.SampleRate == 0 {
				s.format = format
				// Initialize the speaker with a buffer size (1/10th of a second)
				err := speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/10))
				if err != nil {
					_ = streamer.Close()
					_ = f.Close()
					return err
				}
			}

			buffer := beep.NewBuffer(format)
			buffer.Append(streamer)
			_ = streamer.Close()
			_ = f.Close()

			s.buffers[st] = append(s.buffers[st], buffer)
		}
	}
	return nil
}

func (s *Saber) Play(st SaberSound) {
	buffs := s.buffers[st]
	if len(buffs) == 0 {
		return
	}
	idx := rand.IntN(len(buffs))
	speaker.Play(buffs[idx].Streamer(0, buffs[idx].Len()))
}

func (s *Saber) PlaySync(st SaberSound) {
	buffs := s.buffers[st]
	if len(buffs) == 0 {
		return
	}
	idx := rand.IntN(len(buffs))
	done := make(chan bool)
	speaker.Play(beep.Seq(buffs[idx].Streamer(0, buffs[idx].Len()), beep.Callback(func() {
		done <- true
	})))
	<-done
}

func (s *Saber) RunIdleLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			buffs := s.buffers[SoundIdle]
			if len(buffs) > 0 {
				idx := rand.IntN(len(buffs))
				done := make(chan bool)
				speaker.Play(beep.Seq(buffs[idx].Streamer(0, buffs[idx].Len()), beep.Callback(func() {
					done <- true
				})))
				select {
				case <-done:
				case <-ctx.Done():
					return
				}
			} else {
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
}

func (s *Saber) ProcessMovement(x, y, z float64, t float64) {
	// 1. Process with detector for movement
	s.detector.Process(x, y, z, t)

	now := time.Now()
	if len(s.detector.Events) > 0 {
		ev := s.detector.Events[len(s.detector.Events)-1]
		// Use a fixed cooldown to avoid repeated triggers
		if now.Sub(s.lastHit) > 250*time.Millisecond {
			if ev.Amplitude > hitThreshold {
				s.Play(SoundHit)
				s.lastHit = now
			} else if ev.Amplitude > strikeThreshold {
				s.Play(SoundStrike)
				s.lastHit = now
			}
		}
	}

	// 2. Original MacSaber swing detection logic
	// Map library axes to original MacSaber "roll" and "tilt"
	// In MacSaber.m:
	// tilt = gyro[2] * 2; (Z axis)
	// roll = gyro[0]; (X axis)

	roll := x
	tilt := z * 2.0

	// Calculate difference (equivalent to MacSaber.m logic)
	rollD := roll - s.roll2
	tiltD := tilt - s.tilt2

	// deltaD = abs(rollD) < abs(tiltD) ? rollD : tiltD;
	deltaD := rollD
	if math.Abs(tiltD) < math.Abs(rollD) {
		deltaD = tiltD
	}

	// Trigger swing
	if math.Abs(deltaD) > swingThreshold && now.Sub(s.lastSwing) > 300*time.Millisecond {
		s.Play(SoundSwing)
		s.lastSwing = now
	}

	// Update previous values for swing logic
	s.roll2 = roll
	s.tilt2 = tilt
}

func main() {
	if os.Geteuid() != 0 {
		fmt.Println("MacSaber requires root privileges for accelerometer access. Run with: sudo ./MacSaber")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	saber := NewSaber()
	err := saber.LoadSounds(soundFS, "sounds")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error loading sounds: %v\n", err)
		os.Exit(1)
	}

	accelRing, err := shm.CreateRing(shm.NameAccel)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error creating accel ring: %v\n", err)
		os.Exit(1)
	}
	defer func(accelRing *shm.RingBuffer) {
		_ = accelRing.Close()
	}(accelRing)
	defer func(accelRing *shm.RingBuffer) {
		_ = accelRing.Unlink()
	}(accelRing)

	sensorErr := make(chan error, 1)
	go func() {
		if err := sensor.Run(sensor.Config{
			AccelRing: accelRing,
		}); err != nil {
			sensorErr <- err
		}
	}()

	// Wait for the sensor to warm up
	time.Sleep(500 * time.Millisecond)

	fmt.Println("MacSaber started! Move your Mac to swing the saber. Press Ctrl+C to exit.")

	saber.Play(SoundStart)

	// Idle sound loop in the background
	go saber.RunIdleLoop(ctx)

	var lastAccelTotal uint64
	ticker := time.NewTicker(sensorPollInterval)
	defer ticker.Stop()

	// Capture start time for relative timestamps
	startTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nTurning off...")
			saber.PlaySync(SoundOff)
			return
		case err := <-sensorErr:
			_, _ = fmt.Fprintf(os.Stderr, "Sensor error: %v\n", err)
			return
		case <-ticker.C:
		}

		now := time.Now()
		tNow := float64(now.Sub(startTime)) / float64(time.Second)

		samples, newTotal := accelRing.ReadNew(lastAccelTotal, shm.AccelScale)
		lastAccelTotal = newTotal

		if len(samples) == 0 {
			continue
		}

		// Process all new samples
		nSamples := len(samples)
		for idx, sample := range samples {
			// Estimate sample time based on detector's assumed FS (usually 100Hz or 200Hz)
			tSample := tNow - float64(nSamples-idx-1)*0.005 // assuming 200Hz
			saber.ProcessMovement(sample.X, sample.Y, sample.Z, tSample)
		}
	}
}
