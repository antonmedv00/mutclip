package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"mutclip.server/pkg/clipservice"
	"mutclip.server/pkg/net"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const ConnDeadline = time.Minute

func main() {
	gin.DefaultWriter = io.Discard
	gin.SetMode(gin.ReleaseMode)

	r := gin.Default()

	s := clipservice.NewService()

	origins := make(map[string]struct{})
	for _, o := range strings.Split(os.Getenv("ORIGINS"), " ") {
		origins[o] = struct{}{}
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			origin, err := url.Parse(r.Header.Get("Origin"))
			if err != nil {
				log.Error(err)
				return false
			}

			_, ok := origins[origin.Hostname()]
			if !ok {
				log.Errorf("origin %v denied", origin.Hostname())
			}
			return ok
		},
	}

	r.GET("/newclip", func(c *gin.Context) {
		id := s.Generate(c)

		go s.Start(id)

		c.String(200, id)
	})

	r.GET("/check/:id", func(c *gin.Context) {
		id := c.Param("id")
		if s.Check(id) {
			c.Status(200)
		} else {
			c.Status(404)
		}
	})

	r.GET("/ws/:id", func(c *gin.Context) {
		id := c.Param("id")

		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Error(err)
			c.AbortWithStatus(500)
			return
		}

		client, err := s.Connect(id, c.Request.Context())
		if err != nil {
			log.Error(err)
			conn.WriteMessage(websocket.BinaryMessage, net.Out(net.Fatal(err)))
			return
		}

		timer := time.NewTimer(ConnDeadline)
		go func() {
			select {

			case <-timer.C:
				log.Error("websocket deadline expired")

			case <-client.Done():

			}

			client.Cancel()
		}()

		go func() {
			defer client.Cancel()

			for {
				typ, buf, err := conn.ReadMessage()
				if err != nil {
					if _, ok := err.(*websocket.CloseError); ok {
						return
					}

					select {

					case <-client.Done():
						return

					default:

					}

					log.Error(err)
					return
				}

				timer.Reset(ConnDeadline)

				switch typ {

				case websocket.BinaryMessage:
					m, err := net.In(client.Cid, buf)
					if err != nil {
						log.Errorf("unable to parse protobuf message: %v", err)
						client.Out <- net.Err(fmt.Errorf("unexpected message"))
						continue
					}

					client.In <- *m

				case websocket.CloseMessage:
					return

				default:
					log.Errorf("unexpected message of type %v: %v", typ, buf)
					client.Out <- net.Err(fmt.Errorf("unexpected message"))

				}
			}
		}()

		go func() {
			defer client.Cancel()

			for m := range client.Out {
				err := conn.WriteMessage(websocket.BinaryMessage, net.Out(m))
				if err != nil {
					select {

					case <-client.Done():
						return

					default:

					}

					log.Error(err)
					return
				}
			}
		}()

		<-client.Done()

		err = conn.Close()
		if err != nil {
			log.Error(err)
		}
	})

	log.Infof("Server started on port 5000")
	err := r.Run(":5000")
	if err != nil {
		log.Error(err)
	}
}
