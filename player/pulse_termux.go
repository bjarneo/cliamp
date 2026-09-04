//go:build termux

package player

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"github.com/gopxl/beep/v2"
	pulseproto "github.com/jfreymuth/pulse/proto"
)

var (
	errTermuxPulseConnectionClosed = errors.New("pulseaudio: connection closed")
	errTermuxPulseClientClosed     = errors.New("pulseaudio: client closed")
)

const termuxPulseHealthInterval = 250 * time.Millisecond

type termuxPulseClient struct {
	protocol *pulseproto.Client
	conn     net.Conn

	mu       sync.Mutex
	playback map[uint32]*termuxPulsePlayback

	done     chan struct{}
	lostOnce sync.Once
}

func newTermuxSession(sampleRate beep.SampleRate, bufferSize int, fill func([]float32) (int, error)) (*termuxSession, error) {
	client, err := newTermuxPulseClient(pulseServerOption())
	if err != nil {
		return nil, err
	}
	stream, err := client.newPlayback(sampleRate, bufferSize, fill)
	if err != nil {
		client.Close()
		return nil, err
	}
	return &termuxSession{
		stream: stream,
		closeFn: func() {
			stream.Close()
			client.Close()
		},
	}, nil
}

func newTermuxPulseClient(server string) (*termuxPulseClient, error) {
	protocol, conn, err := pulseproto.Connect(server)
	if err != nil {
		return nil, err
	}
	client := &termuxPulseClient{
		protocol: protocol,
		conn:     conn,
		playback: make(map[uint32]*termuxPulsePlayback),
		done:     make(chan struct{}),
	}
	protocol.Callback = client.callback

	props := pulseproto.PropList{
		"media.name":                 pulseproto.PropListString("cliamp"),
		"application.name":           pulseproto.PropListString("cliamp"),
		"application.icon_name":      pulseproto.PropListString("audio-x-generic"),
		"application.process.id":     pulseproto.PropListString(strconv.Itoa(os.Getpid())),
		"application.process.binary": pulseproto.PropListString(os.Args[0]),
		"window.x11.display":         pulseproto.PropListString(os.Getenv("DISPLAY")),
	}
	if err := protocol.Request(&pulseproto.SetClientName{Props: props}, &pulseproto.SetClientNameReply{}); err != nil {
		client.Close()
		return nil, err
	}
	if err := protocol.Request(&pulseproto.Subscribe{Mask: pulseproto.SubscriptionMaskSinkInput}, nil); err != nil {
		client.Close()
		return nil, err
	}
	go client.monitor()
	return client, nil
}

func (c *termuxPulseClient) callback(message interface{}) {
	switch message := message.(type) {
	case *pulseproto.Request:
		c.mu.Lock()
		stream := c.playback[message.StreamIndex]
		c.mu.Unlock()
		if stream != nil {
			stream.deliverRequest(int(message.Length))
		}
	case *pulseproto.Started:
		c.mu.Lock()
		stream := c.playback[message.StreamIndex]
		c.mu.Unlock()
		if stream != nil {
			stream.notifyStarted()
		}
	case *pulseproto.Underflow:
		c.mu.Lock()
		stream := c.playback[message.StreamIndex]
		c.mu.Unlock()
		if stream != nil {
			stream.markUnderflow()
		}
	case *pulseproto.ConnectionClosed:
		c.markLost(errTermuxPulseConnectionClosed)
	}
}

func (c *termuxPulseClient) monitor() {
	ticker := time.NewTicker(termuxPulseHealthInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if !c.hasActivePlayback() {
				continue
			}
			var reply pulseproto.GetServerInfoReply
			if err := c.protocol.Request(&pulseproto.GetServerInfo{}, &reply); err != nil {
				c.markLost(err)
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *termuxPulseClient) hasActivePlayback() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, stream := range c.playback {
		if stream.isRunning() {
			return true
		}
	}
	return false
}

func (c *termuxPulseClient) newPlayback(sampleRate beep.SampleRate, bufferSize int, fill func([]float32) (int, error)) (*termuxPulsePlayback, error) {
	targetLength := uint32(bufferSize) * 2 * 4
	request := pulseproto.CreatePlaybackStream{
		SampleSpec: pulseproto.SampleSpec{
			Format:   pulseproto.FormatFloat32LE,
			Channels: 2,
			Rate:     uint32(sampleRate),
		},
		ChannelMap:            pulseproto.ChannelMap{pulseproto.ChannelLeft, pulseproto.ChannelRight},
		SinkIndex:             pulseproto.Undefined,
		BufferMaxLength:       targetLength * 2,
		Corked:                true,
		BufferTargetLength:    targetLength,
		BufferPrebufferLength: pulseproto.Undefined,
		BufferMinimumRequest:  pulseproto.Undefined,
		ChannelVolumes:        pulseproto.ChannelVolumes{0x100, 0x100},
		AdjustLatency:         true,
		Properties: pulseproto.PropList{
			"media.name": pulseproto.PropListString("cliamp"),
		},
	}
	var reply pulseproto.CreatePlaybackStreamReply
	if err := c.protocol.Request(&request, &reply); err != nil {
		return nil, err
	}

	stream := &termuxPulsePlayback{
		client:             c,
		index:              reply.StreamIndex,
		bufferTargetLength: reply.BufferTargetLength,
		bufferMaxLength:    reply.BufferMaxLength,
		reader:             termuxPulseFloat32Reader(fill),
		request:            make(chan int),
		started:            make(chan struct{}, 1),
		done:               make(chan struct{}),
		state:              termuxPulseIdle,
	}
	if stream.bufferTargetLength == 0 {
		stream.bufferTargetLength = targetLength
	}
	if stream.bufferMaxLength == 0 {
		stream.bufferMaxLength = targetLength * 2
	}
	c.mu.Lock()
	c.playback[stream.index] = stream
	c.mu.Unlock()
	go stream.run()
	return stream, nil
}

func (c *termuxPulseClient) remove(stream *termuxPulsePlayback) {
	c.mu.Lock()
	delete(c.playback, stream.index)
	c.mu.Unlock()
}

func (c *termuxPulseClient) markLost(err error) {
	if err == nil {
		err = errTermuxPulseConnectionClosed
	}
	c.lostOnce.Do(func() {
		c.mu.Lock()
		streams := make([]*termuxPulsePlayback, 0, len(c.playback))
		for _, stream := range c.playback {
			streams = append(streams, stream)
		}
		c.playback = make(map[uint32]*termuxPulsePlayback)
		c.mu.Unlock()

		for _, stream := range streams {
			stream.markLost(err)
		}
		_ = c.conn.Close()
		close(c.done)
	})
}

func (c *termuxPulseClient) Close() {
	c.markLost(errTermuxPulseClientClosed)
}

type termuxPulsePlaybackState uint8

const (
	termuxPulseIdle termuxPulsePlaybackState = iota
	termuxPulseRunning
	termuxPulsePaused
	termuxPulseLost
	termuxPulseClosed
)

type termuxPulsePlayback struct {
	client             *termuxPulseClient
	index              uint32
	bufferTargetLength uint32
	bufferMaxLength    uint32
	reader             termuxPulseReader
	request            chan int
	started            chan struct{}
	done               chan struct{}

	stateMu   sync.RWMutex
	state     termuxPulsePlaybackState
	err       error
	underflow bool

	startMu sync.Mutex
	closeMu sync.Once
	doneMu  sync.Once
}

func (p *termuxPulsePlayback) StartContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	p.startMu.Lock()
	defer p.startMu.Unlock()

	p.stateMu.Lock()
	switch p.state {
	case termuxPulseClosed, termuxPulseLost:
		err := p.err
		p.stateMu.Unlock()
		if err == nil {
			err = errTermuxPulseConnectionClosed
		}
		return err
	case termuxPulseRunning, termuxPulsePaused:
		p.stateMu.Unlock()
		return nil
	default:
		p.state = termuxPulseRunning
		p.err = nil
		p.underflow = false
	}
	p.stateMu.Unlock()

	if err := p.client.protocol.Request(&pulseproto.FlushPlaybackStream{StreamIndex: p.index}, nil); err != nil {
		p.fail(err)
		return err
	}
	select {
	case p.request <- int(p.bufferTargetLength):
	case <-p.done:
		return p.Error()
	case <-ctx.Done():
		p.resetIdle()
		return ctx.Err()
	}
	if err := p.client.protocol.Request(&pulseproto.CorkPlaybackStream{StreamIndex: p.index, Corked: false}, nil); err != nil {
		p.fail(err)
		return err
	}
	select {
	case <-p.started:
		return nil
	case <-p.done:
		return p.Error()
	case <-ctx.Done():
		p.resetIdle()
		return ctx.Err()
	}
}

func (p *termuxPulsePlayback) Done() <-chan struct{} { return p.done }

func (p *termuxPulsePlayback) Pause() {
	p.stateMu.RLock()
	running := p.state == termuxPulseRunning
	p.stateMu.RUnlock()
	if !running {
		return
	}
	if err := p.client.protocol.Request(&pulseproto.CorkPlaybackStream{StreamIndex: p.index, Corked: true}, nil); err != nil {
		p.client.markLost(err)
		return
	}
	p.stateMu.Lock()
	if p.state == termuxPulseRunning {
		p.state = termuxPulsePaused
	}
	p.stateMu.Unlock()
}

func (p *termuxPulsePlayback) Resume() {
	p.stateMu.RLock()
	paused := p.state == termuxPulsePaused
	p.stateMu.RUnlock()
	if !paused {
		return
	}
	if err := p.client.protocol.Request(&pulseproto.CorkPlaybackStream{StreamIndex: p.index, Corked: false}, nil); err != nil {
		p.client.markLost(err)
		return
	}
	p.stateMu.Lock()
	if p.state == termuxPulsePaused {
		p.state = termuxPulseRunning
		p.underflow = false
	}
	p.stateMu.Unlock()
}

func (p *termuxPulsePlayback) Close() {
	p.closeMu.Do(func() {
		p.stateMu.Lock()
		p.state = termuxPulseClosed
		if p.err == nil {
			p.err = errTermuxPulseClientClosed
		}
		p.stateMu.Unlock()
		p.signalDone()
		p.client.remove(p)
		_ = p.client.protocol.Request(&pulseproto.DeletePlaybackStream{StreamIndex: p.index}, nil)
	})
}

func (p *termuxPulsePlayback) Error() error {
	p.stateMu.RLock()
	err := p.err
	p.stateMu.RUnlock()
	if err == nil {
		return errTermuxPulseConnectionClosed
	}
	return err
}

func (p *termuxPulsePlayback) run() {
	requested := 0
	front := make([]byte, p.bufferMaxLength)
	back := make([]byte, p.bufferMaxLength)
	for {
		select {
		case bufferLength := <-p.request:
			if !p.isRunning() {
				requested = 0
				continue
			}
			requested += bufferLength
			for requested > 0 {
				if requested > len(front) {
					front = make([]byte, requested)
					back = make([]byte, requested)
				}
				readCount, err := p.reader.Read(front[:requested])
				if err != nil {
					p.fail(err)
					return
				}
				if readCount > 0 {
					if err := p.client.protocol.Send(p.index, front[:readCount]); err != nil {
						p.client.markLost(err)
						return
					}
					requested -= readCount
					front, back = back, front
				}
				select {
				case nextLength := <-p.request:
					requested += nextLength
				case <-p.done:
					return
				default:
				}
			}
		case <-p.done:
			return
		}
	}
}

func (p *termuxPulsePlayback) deliverRequest(length int) {
	select {
	case p.request <- length:
	case <-p.done:
	}
}

func (p *termuxPulsePlayback) notifyStarted() {
	p.stateMu.RLock()
	ready := p.state == termuxPulseRunning
	p.stateMu.RUnlock()
	if !ready {
		return
	}
	select {
	case p.started <- struct{}{}:
	default:
	}
}

func (p *termuxPulsePlayback) markUnderflow() {
	p.stateMu.Lock()
	if p.state == termuxPulseRunning {
		p.underflow = true
	}
	p.stateMu.Unlock()
}

func (p *termuxPulsePlayback) markLost(err error) {
	p.stateMu.Lock()
	if p.state != termuxPulseClosed {
		p.state = termuxPulseLost
		p.err = err
	}
	p.stateMu.Unlock()
	p.signalDone()
}

func (p *termuxPulsePlayback) fail(err error) {
	p.markLost(err)
}

func (p *termuxPulsePlayback) resetIdle() {
	p.stateMu.Lock()
	if p.state == termuxPulseRunning {
		p.state = termuxPulseIdle
	}
	p.stateMu.Unlock()
}

func (p *termuxPulsePlayback) isRunning() bool {
	p.stateMu.RLock()
	running := p.state == termuxPulseRunning
	p.stateMu.RUnlock()
	return running
}

func (p *termuxPulsePlayback) signalDone() {
	p.doneMu.Do(func() { close(p.done) })
}

type termuxPulseReader interface {
	Read([]byte) (int, error)
}

type termuxPulseFloat32Reader func([]float32) (int, error)

func (r termuxPulseFloat32Reader) Read(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	values := unsafe.Slice((*float32)(unsafe.Pointer(&buf[0])), len(buf)/4)
	n, err := r(values)
	return n * 4, err
}

func (termuxPulseFloat32Reader) Format() byte { return pulseproto.FormatFloat32LE }
