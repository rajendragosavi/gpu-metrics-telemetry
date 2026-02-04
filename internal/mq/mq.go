package mq

type Message []byte

type Producer interface {
	Publish(topic string, msg Message) error
}

type Consumer interface {
	Subscribe(topic string, handler func(Message)) error
	Close() error
}
