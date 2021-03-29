package uevent

type Message struct {
	Action     string
	DevicePath string
	Attributes map[string]string
}
