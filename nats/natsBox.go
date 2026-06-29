package natsHandler

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"

	"github.com/nats-io/nats.go"
)

const (
	NATS_DEFAULT_INBOX = "_INBOX"
)

type Nats struct {
	cred        string
	url string
	con         *nats.Conn
	inbox string
	account string
}

func New(cred ,url string) (*Nats, error) {
	n := Nats{cred: cred, url: url,inbox: NATS_DEFAULT_INBOX}
	if err := n.extractToken(); err != nil {
		return nil, err
	}
	err := n.Connect()
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (n *Nats) extractToken() error {
	var credBytes []byte
	var err error

		credBytes, err = base64.StdEncoding.DecodeString(n.cred)
		if err != nil {
			return err
		}
		n.cred = string(credBytes)
	
	token, err := jwt.ParseDecoratedJWT(credBytes)
	if err != nil {
		return err
	}
	userClaim, err := jwt.DecodeUserClaims(token)
	if err != nil {
		return err
	}
	n.account = userClaim.Issuer
	if tags := userClaim.GetTags(); len(tags) > 0 {
		for _, v := range tags {
			if strings.HasPrefix(v, NATS_DEFAULT_INBOX) {
				n.inbox = v
			}

		}
	}
	return nil
}
func (n *Nats) Connect() error {
	var err error
	n.con, err = nats.Connect(fmt.Sprintf("nats://%s", n.url),
		nats.RetryOnFailedConnect(true),
		nats.CustomInboxPrefix(n.inbox),
		nats.UserCredentialBytes([]byte(n.cred)),
		nats.PingInterval(30*time.Second),
		nats.ReconnectHandler(func(c *nats.Conn) {
			// Reconnect logic
			fmt.Printf("Reconnected to NATS server: %s\n", c.ConnectedUrl())
		}), nats.ReconnectErrHandler(func(c *nats.Conn, err error) {
			fmt.Printf("Reconnection error: %v", err)
		}), nats.DisconnectErrHandler(func(c *nats.Conn, err error) {
			fmt.Printf("Disconnected from NATS server: %v", err)
		}), nats.ClosedHandler(func(c *nats.Conn) {
			fmt.Printf("Connection to NATS server closed")
		}))
	if err != nil {
		return err
	}
	return nil
}

func (n *Nats) GetConnection() *nats.Conn {
	if n.con == nil {
		n.Connect()
	}
	if n.con.IsClosed() {
		n.Connect()
	}
	return n.con
}