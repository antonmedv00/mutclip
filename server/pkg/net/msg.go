package net

import (
	pb "mutclip.server/pkg/pb/clip"

	"google.golang.org/protobuf/proto"
)

type InMessage struct {
	*pb.Message
	Cid CID
}

type OutMessage = *pb.Message

func In(cid CID, b []byte) (*InMessage, error) {
	m := &pb.Message{}

	err := proto.Unmarshal(b, m)
	if err != nil {
		return nil, err
	}

	return &InMessage{Cid: cid, Message: m}, nil
}

func Out(m OutMessage) []byte {
	b, err := proto.Marshal(m)
	if err != nil {
		panic(err)
	}

	return b
}

func Err(err error) OutMessage {
	return &pb.Message{Msg: &pb.Message_Err{Err: &pb.Error{Desc: err.Error(), Fatal: false}}}
}

func Fatal(err error) OutMessage {
	return &pb.Message{Msg: &pb.Message_Err{Err: &pb.Error{Desc: err.Error(), Fatal: true}}}
}
