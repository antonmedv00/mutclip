package clipservice

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"

	"mutclip.server/pkg/fail"
	"mutclip.server/pkg/net"
	pb "mutclip.server/pkg/pb/clip"

	"github.com/charmbracelet/log"
)

const (
	ClipDeadline = time.Minute * 2
	alphabet     = "abcdefghijklmnopqrstuvwxyz"
)

type ClipboardService struct {
	clips sync.Map
}

type ClipboardId = string

type Clipboard struct {
	router  *net.Router
	content Content
	clients map[net.CID]struct{} // FIXME
	ctx     context.Context
	cancel  context.CancelFunc
}

type Client struct {
	context.Context
	Cancel context.CancelFunc

	Cid net.CID

	In  chan net.InMessage
	Out chan net.OutMessage
}

type Content any

type ContentText struct {
	data string
}

type ContentFile struct {
	ready          bool
	chunks         [][]byte
	nextChunkIndex int
	numChunks      int
	contentType    string
	filename       string
}

func NewService() *ClipboardService {
	return &ClipboardService{}
}

func (s *ClipboardService) Generate(ctx context.Context) ClipboardId {
	id := ""
	for {
		var parts []any
		for range 6 {
			x := rand.N(len(alphabet) + 10)
			if x < len(alphabet) {
				parts = append(parts, string(alphabet[x]))
			} else {
				parts = append(parts, strconv.Itoa(x-len(alphabet)))
			}
		}
		id = fmt.Sprintf("%v%v-%v%v-%v%v", parts...)

		_, exists := s.clips.Load(id)
		if !exists {
			break
		}
	}

	clipCtx, clipCancel := context.WithCancel(ctx)

	router := net.NewRouter(clipCtx)

	clipboard := &Clipboard{
		router:  router,
		content: ContentText{},
		clients: make(map[net.CID]struct{}),
		ctx:     clipCtx,
		cancel:  clipCancel,
	}

	s.clips.Store(id, clipboard)
	log.Infof("* GEN %v", id)

	go func() {
		<-clipCtx.Done()

		time.Sleep(time.Millisecond * 10)

		s.clips.Delete(id)
		log.Infof("* END %v", id)
	}()

	return id
}

func (s *ClipboardService) getClip(id ClipboardId) *Clipboard {
	a, ok := s.clips.Load(id)
	if !ok {
		return nil
	}

	clip, ok := a.(*Clipboard)
	if !ok {
		panic("impossible")
	}

	return clip
}

func (s *ClipboardService) Check(id ClipboardId) bool {
	return s.getClip(id) != nil
}

func (s *ClipboardService) Connect(id ClipboardId, ctx context.Context) (*Client, error) {
	clip := s.getClip(id)
	if clip == nil {
		return nil, fail.Fail(4, "Clipboard with id of %v does not exist", id)
	}

	clientCtx, clientCancel := context.WithCancel(ctx)

	out := make(chan net.OutMessage, 15)

	cid := clip.router.Connect(out, clientCtx)

	client := &Client{
		Context: clientCtx,
		Cancel:  clientCancel,
		Cid:     cid,
		In:      clip.router.Source,
		Out:     out,
	}

	clip.clients[cid] = struct{}{}
	log.Infof("[%v] + %v", id, cid)

	go func() {
		select {
		case <-clientCtx.Done():
		case <-clip.ctx.Done():
		}

		clientCancel()
	}()

	go func() {
		<-clientCtx.Done()

		close(out)

		delete(clip.clients, cid)
		log.Infof("[%v] - %v", id, cid)
	}()

	go func() {
		time.Sleep(time.Millisecond)

		err := s.syncClient(id, cid)
		if err != nil {
			_ = fail.Scope(err, "Error while synchronizing contents of clipboard %v", id).Error()
		}
	}()

	return client, nil
}

func (s *ClipboardService) syncClient(id ClipboardId, cid net.CID) error {
	clip := s.getClip(id)
	r := clip.router

	switch content := clip.content.(type) {

	case ContentText:
		text := "<empty>"
		if content.data != "" {
			text = content.data
		}
		log.Infof("[%v] SYNC -> %v : TXT %v", id, cid, text)

		err := r.Send(cid, &pb.Message{Msg: &pb.Message_Text{Text: &pb.Text{Data: content.data}}})
		if err != nil {
			if errors.Is(err, net.ErrInvalidCid) {
				return fail.Fail(12, "Client %v disconnected", cid)
			}

			panic("impossible")
		}

		return nil

	case ContentFile:
		log.Infof("[%v] SYNC -> %v : FILE %v/%v", id, cid, content.filename, content.numChunks)

		if !content.ready {
			log.Warnf("[%v] SYNC -> %v : FILE not ready", id, cid)
			return nil
		}

		tun, err := r.Tunnel(cid)
		if err != nil {
			if errors.Is(err, net.ErrInvalidCid) {
				return fail.Fail(12, "Client %v disconnected", cid)
			}

			if errors.Is(err, net.ErrDuplicateTun) {
				return fail.Fail(13, "Client %v is busy", cid)
			}

			panic("impossible")
		}
		defer tun.Cancel()

		tun.Out <- &pb.Message{Msg: &pb.Message_Hdr{Hdr: &pb.FileHeader{
			Filename:    content.filename,
			ContentType: content.contentType,
			NumChunks:   int32(content.numChunks),
		}}}

		idx := 0
		for m := range tun.In {
			if m.GetNextChunk() == nil {
				tun.Out <- net.Err(
					fail.Wrap(
						fail.SomethingWentWrong("Unexpected message: %v", m),
						14,
						"Server expected request for next chunk while sending file to %v", cid,
					),
				)
				continue
			}

			log.Infof("[%v] SYNC -> %v : %v/%v", id, cid, idx+1, content.numChunks)

			tun.Out <- &pb.Message{Msg: &pb.Message_Chunk{Chunk: &pb.Chunk{Index: int32(idx), Data: content.chunks[idx]}}}
			idx++

			if idx < content.numChunks {
				continue
			}

			log.Infof("[%v] SYNC -> %v : OK", id, cid)
			return nil
		}

		return fail.Fail(15, "Client %v disconnected while receiving file", cid)

	default:
		panic("impossible")

	}
}

func (s *ClipboardService) syncClip(id ClipboardId, srcCid net.CID) {
	clip := s.getClip(id)
	r := clip.router

	switch content := clip.content.(type) {

	case ContentText:
		text := "<empty>"
		if content.data != "" {
			text = content.data
		}
		log.Infof("[%v] SYNC -> @ : TXT %v", id, text)

		r.Broadcast(
			&pb.Message{Msg: &pb.Message_Text{Text: &pb.Text{Data: content.data}}},
			map[net.CID]struct{}{srcCid: {}},
		)

	case ContentFile:
		wg := sync.WaitGroup{}
		for cid := range clip.clients {
			if cid == srcCid {
				continue
			}

			wg.Add(1)

			go func() {
				defer wg.Done()

				err := s.syncClient(id, cid)
				if err != nil {
					_ = fail.Scope(err, "Error while synchronizing update from %v", srcCid).Error()
				}
			}()
		}

		wg.Wait()

	default:
		panic("impossible")

	}

	log.Infof("[%v] ACK => %v", id, srcCid)
	err := r.Send(srcCid, &pb.Message{Msg: &pb.Message_Ack{Ack: &pb.Ack{}}})
	if err != nil {
		if errors.Is(err, net.ErrInvalidCid) {
			_ = fail.Fail(16, "Client %v disconnected while updating clipboard", srcCid).Error()
		} else {
			panic("impossible")
		}
	}
}

func (s *ClipboardService) processText(id ClipboardId, cid net.CID, m *pb.Text) {
	clip := s.getClip(id)
	r := clip.router

	data := m.GetData()

	text := data
	if text == "" {
		text = "<empty>"
	}
	log.Infof("[%v] $ <= %v : TXT %v", id, cid, text)

	if file, ok := clip.content.(ContentFile); ok {
		if !file.ready {
			_ = fail.Fail(17, "Update from %v denied while a file is received", cid).Error()

			// TODO: send error here?

			err := r.Send(cid, &pb.Message{Msg: &pb.Message_Ack{Ack: &pb.Ack{}}})
			if err != nil {
				if errors.Is(err, net.ErrInvalidCid) {
					_ = fail.Fail(18, "Client %v disconnected while sending update", cid).Error()
				} else {
					panic("impossible")
				}
			}

			return
		}
	}

	clip.content = ContentText{data}
	s.syncClip(id, cid)
}

func (s *ClipboardService) processFile(id ClipboardId, timer *time.Timer, cid net.CID, m *pb.FileHeader) {
	clip := s.getClip(id)
	r := clip.router

	log.Infof("[%v] $ <= %v : FILE %v/%v", id, cid, m.GetFilename(), m.GetNumChunks())

	if file, ok := clip.content.(ContentFile); ok {
		if !file.ready {
			_ = fail.Fail(17, "Update from %v denied while a file is received", cid).Error()

			// TODO: also send error?

			err := r.Send(cid, &pb.Message{Msg: &pb.Message_Ack{Ack: &pb.Ack{}}})
			if err != nil {
				if errors.Is(err, net.ErrInvalidCid) {
					_ = fail.Fail(18, "Client %v disconnected while sending update", cid).Error()
				} else {
					panic("impossible")
				}
			}

			return
		}
	}

	originalContent := clip.content

	clip.content = ContentFile{
		filename:    m.GetFilename(),
		contentType: m.GetContentType(),
		numChunks:   int(m.GetNumChunks()),
	}

	tun, err := r.Tunnel(cid)
	if err != nil {
		if errors.Is(err, net.ErrInvalidCid) {
			_ = fail.Fail(21, "Client %v disconnected while sending file", cid).Error()
			return
		}

		if errors.Is(err, net.ErrDuplicateTun) {
			_ = fail.Fail(22, "Client %v is busy and unable to send file", cid).Error()
			return
		}

		panic("impossible")
	}
	defer tun.Cancel()

	tun.Out <- &pb.Message{Msg: &pb.Message_NextChunk{NextChunk: &pb.NextChunk{}}}

	for m := range tun.In {
		timer.Reset(ClipDeadline) // FIXME

		chunk := m.GetChunk()
		if chunk == nil {
			tun.Out <- net.Err(
				fail.Wrap(fail.SomethingWentWrong("Unexpected message: %v", m), 19, "Server expected chunk while receiving file from %v", cid),
			)
			return
		}

		file, ok := clip.content.(ContentFile)
		if !ok {
			tun.Out <- net.Err(
				fail.Scope(fail.SomethingWentWrong("Unexpected state of clipboard contents: %v", clip.content), "Error while receiving file from %v", cid),
			)
			return
		}

		if int(chunk.GetIndex()) != file.nextChunkIndex {
			tun.Out <- net.Err(
				fail.Fail(20, "Server received chunk with index %v, but expected %v", chunk.GetIndex(), file.nextChunkIndex),
			)
			return
		}

		log.Infof("[%v] <- %v : %v/%v", id, cid, chunk.GetIndex()+1, file.numChunks)

		file.nextChunkIndex++
		file.chunks = append(file.chunks, chunk.GetData())

		if file.nextChunkIndex < file.numChunks {
			clip.content = file
			tun.Out <- &pb.Message{Msg: &pb.Message_NextChunk{NextChunk: &pb.NextChunk{}}}
			continue
		}

		file.ready = true
		clip.content = file

		log.Infof("[%v] <- %v : OK", id, cid)
		s.syncClip(id, cid)
		return
	}

	clip.content = originalContent
	_ = fail.Fail(23, "Client %v disconnected while sending file", cid).Error()
}

func (s *ClipboardService) Start(id ClipboardId) {
	clip := s.getClip(id)
	r := clip.router

	go r.Start()

	log.Infof("* START %v", id)

	timer := time.NewTimer(ClipDeadline)
	go func() {
		select {

		case <-timer.C:
			log.Warn("clip deadline expired")

		case <-clip.ctx.Done():

		}

		clip.cancel()
	}()

	for m := range r.Drain {
		timer.Reset(ClipDeadline)

		if text := m.GetText(); text != nil {
			s.processText(id, m.Cid, text)
			continue
		}

		if hdr := m.GetHdr(); hdr != nil {
			go s.processFile(id, timer, m.Cid, hdr)
			continue
		}

		err := r.Send(m.Cid, net.Err(
			fail.Wrap(
				fail.SomethingWentWrong("Unexpected message: %v", m),
				24,
				"Server expected text or file update from %v", m.Cid,
			),
		))
		if err != nil {
			if errors.Is(err, net.ErrInvalidCid) {
				_ = fail.Fail(25, "Client %v disconnected", m.Cid).Error()
				continue
			}

			panic("impossible")
		}
	}
}
