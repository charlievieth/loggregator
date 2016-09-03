package main

import (
	"io"
	"testing"
)

const MessageCount = 20

var (
	Writer   *io.PipeWriter
	Reader   *io.PipeReader
	Messages [][]byte
)

func init() {
	var err error
	Messages, err = generateMessages(MessageCount)
	if err != nil {
		panic(err)
	}
	Reader, Writer = io.Pipe()
	if err := StartMetron(mustParseConfig(), Reader); err != nil {
		panic(err)
	}
}

func BenchmarkMetron(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for i := 0; pb.Next(); i++ {
			n := i % len(Messages)
			if _, err := Writer.Write(Messages[n]); err != nil {
				b.Fatal(err)
			}
		}
	})
}
