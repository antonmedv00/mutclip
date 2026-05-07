package main

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"mutclip.server/pkg/clipservice"
	"mutclip.server/pkg/fail"
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
				_ = fail.Wrap(err, 1, "Error while parsing Origin header").Error()
				return false
			}

			_, ok := origins[origin.Hostname()]
			if !ok {
				_ = fail.Fail(2, "Origin %v denied", origin.Hostname()).Error()
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
			c.String(500, fail.Wrap(err, 3, "[%v] Error while upgrading to websocket", id).Error())
			return
		}

		client, err := s.Connect(id, c.Request.Context())
		if err != nil {
			conn.WriteMessage(
				websocket.BinaryMessage,
				net.Out(net.Err(fail.Scope(err, "[%v] Error while connecting to clipboard", id))),
			)
			return
		}

		timer := time.NewTimer(ConnDeadline)
		go func() {
			select {

			case <-timer.C:
				log.Warn("websocket deadline expired")

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

					_ = fail.Wrap(err, 5, "[%v] Error while reading websocket message", id).Error()
					return
				}

				timer.Reset(ConnDeadline)

				switch typ {

				case websocket.BinaryMessage:
					m, err := net.In(client.Cid, buf)
					if err != nil {
						client.Out <- net.Err(fail.Wrap(err, 7, "[%v] Error while parsing protobuf message", id))
						continue
					}

					client.In <- *m

				case websocket.CloseMessage:
					return

				default:
					client.Out <- net.Err(
						fail.Wrap(
							fail.SomethingWentWrong("Unexpected message of type %v", typ),
							8,
							"[%v] Unexpected websocket message", id,
						),
					)

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

					_ = fail.Wrap(err, 9, "[%v] Error while writing websocket message", id).Error()
					return
				}
			}
		}()

		<-client.Done()

		err = conn.Close()
		if err != nil {
			_ = fail.Wrap(err, 10, "[%v] Error while closing websocket connection", id).Error()
		}
	})

	log.Infof("Server started on port 5000")
	err := r.Run(":5000")
	if err != nil {
		_ = fail.Wrap(err, 11, "Error while starting server").Error()
	}
}
