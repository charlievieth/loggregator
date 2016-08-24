package xtron

import (
	"errors"
	"fmt"
	"log"
	"net"
	"sync/atomic"
)

type XTron struct {
}

type readerState int32

const (
	readerStopped = iota
	readerListening
	readerClosed
)

type Reader struct {
	conn  net.PacketConn
	name  string
	log   *log.Logger
	state int32
	ch    chan *Packet
}

type Packet struct {
	Body []byte
	Addr net.Addr
}

func (r *Reader) start() error {
	if r.conn == nil {
		return errors.New("reader: nil PacketConn")
	}
	var err error
	if !atomic.CompareAndSwapInt32(&r.state, readerStopped, readerListening) {
		switch n := atomic.LoadInt32(&r.state); n {
		case readerListening:
			err = errors.New("reader: reader already listening")
		case readerClosed:
			err = errors.New("reader: cannot listen on closed reader")
		default:
			err = fmt.Errorf("reader: unexpected state %d\n", n)
		}
	}
	return err
}

func (r *Reader) Listen() error {
	if err := r.start(); err != nil {
		return err
	}
	buf := make([]byte, 0xFFFF)
	for {
		if atomic.LoadInt32(&r.state) == readerClosed {
			log.Println("listen: closed")
			break
		}
		n, addr, err := r.conn.ReadFrom(buf)
		if err != nil {
			log.Printf("listen: error: %s\n", err)
			return err
		}
		body := make([]byte, n)
		copy(body, buf[:n])
		r.ch <- &Packet{
			Body: body,
			Addr: addr,
		}
	}
	return nil
}
