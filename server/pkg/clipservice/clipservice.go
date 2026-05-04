package clipservice

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"

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

var (
	ErrInvalidClipId      = errors.New("invalid clipboard id")
	ErrClientDisconnected = errors.New("client disconnected while sending file")
)

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
		return nil, ErrInvalidClipId
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
			log.Error(err)
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

		return r.Send(cid, &pb.Message{Msg: &pb.Message_Text{Text: &pb.Text{Data: content.data}}})

	case ContentFile:
		log.Infof("[%v] SYNC -> %v : FILE %v/%v", id, cid, content.filename, content.numChunks)

		if !content.ready {
			log.Warnf("[%v] SYNC -> %v : FILE not ready", id, cid)
			return nil
		}

		tun, err := r.Tunnel(cid)
		if err != nil {
			return err
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
				log.Errorf("unexpected message while sending file: %v", m)
				tun.Out <- net.Err(fmt.Errorf("unexpected message"))
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

		return ErrClientDisconnected

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
					log.Error(err)
				}
			}()
		}

		wg.Wait()

	default:
		panic("impossible")

	}

	err := r.Send(srcCid, &pb.Message{Msg: &pb.Message_Ack{Ack: &pb.Ack{}}})
	if err != nil {
		log.Error(err)
	}

	log.Infof("[%v] ACK => %v", id, srcCid)
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
			log.Error("denied while receiving file")

			// TODO: send error here?

			err := r.Send(cid, &pb.Message{Msg: &pb.Message_Ack{Ack: &pb.Ack{}}})
			if err != nil {
				log.Error(err)
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
			log.Error("denied while receiving file")

			// TODO: also send error?

			err := r.Send(cid, &pb.Message{Msg: &pb.Message_Ack{Ack: &pb.Ack{}}})
			if err != nil {
				log.Error(err)
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
		log.Error(err)
		return
	}
	defer tun.Cancel()

	tun.Out <- &pb.Message{Msg: &pb.Message_NextChunk{NextChunk: &pb.NextChunk{}}}

	for m := range tun.In {
		timer.Reset(ClipDeadline) // FIXME

		chunk := m.GetChunk()
		if chunk == nil {
			log.Errorf("unexpected message while receiving file")
			tun.Out <- net.Err(fmt.Errorf("unexpected message"))
			return
		}

		file, ok := clip.content.(ContentFile)
		if !ok {
			log.Errorf("unexpected state of contents: %v", clip.content)
			tun.Out <- net.Err(fmt.Errorf("internal server error"))
			return
		}

		if int(chunk.GetIndex()) != file.nextChunkIndex {
			log.Errorf("received chunk with index %v, but expected %v", chunk.GetIndex(), file.nextChunkIndex)
			tun.Out <- net.Err(fmt.Errorf("transmission disordered"))
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
	log.Error("client disconnected while receiving file")
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
			log.Error("clip deadline expired")

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

		log.Errorf("unexpected message")
		r.Send(m.Cid, net.Err(fmt.Errorf("unpexpected message")))
	}
}
