package main

import (
	"bytes"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"os"
	"path"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap-idle"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/mail"

	"github.com/microcosm-cc/bluemonday"
)

type Header struct {
	From    []string
	To      []string
	Date    string
	Subject string
	Map     map[string][]string
}

type Attachment struct {
	Name    string
	Content string
}

type Message struct {
	Date       string
	Header     Header
	Body       []string
	Attachment []Attachment
}

type stateT struct {
	imap        string
	mailbox     string
	username    string
	password    string
	template    string
	pollTimeout time.Duration
	verbose     int
	noTLS       bool
}

const (
	version = "0.9.1"
)

var errEOF = errors.New("EOF: IDLE exited")

//go:embed template.txt
var TextTemplate []byte

func getenv(k, def string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return def
}

func args() *stateT {
	flag.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, `%s v%s
Usage: %s [<option>] <server>:<port>

`, path.Base(os.Args[0]), version, os.Args[0])
		flag.PrintDefaults()
	}

	mailbox := flag.String("mailbox", imap.InboxName,
		"IMAP mailbox")
	username := flag.String("username", getenv("IMAPPIPE_USERNAME", ""),
		"IMAP username")
	password := flag.String("password", getenv("IMAPPIPE_PASSWORD", ""),
		"IMAP password")

	template := flag.String("template", "",
		"message template")

	pollTimeout := flag.Duration("poll-timeout", 0,
		"Set poll interval if IDLE not supported")

	noTLS := flag.Bool("no-tls", false, "Disable TLS IMAP")

	verbose := flag.Int("verbose", 0,
		"Enable debug messages")

	flag.Parse()

	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(1)
	}

	tmpl := TextTemplate
	var err error
	if *template != "" {
		tmpl, err = os.ReadFile(*template)
		if err != nil {
			log.Fatalln(err)
		}
	}

	return &stateT{
		imap:        flag.Arg(0),
		mailbox:     *mailbox,
		username:    *username,
		password:    *password,
		template:    string(tmpl),
		pollTimeout: *pollTimeout,
		noTLS:       *noTLS,
		verbose:     *verbose,
	}
}

func main() {
	state := args()
	if err := state.connect(); err != nil {
		log.Fatalln(err)
	}
}

func (state *stateT) dial() (*client.Client, error) {
	if state.noTLS {
		return client.Dial(state.imap)
	}
	return client.DialTLS(state.imap, nil)
}

func (state *stateT) connect() error {
	c, err := state.dial()
	if err != nil {
		return err
	}

	defer func() {
		_ = c.Logout()
	}()

	if err := c.Login(state.username, state.password); err != nil {
		return err
	}

	mbox, err := c.Select(state.mailbox, false)
	if err != nil {
		return err
	}

	for {
		err := state.eventpoll(c, mbox)
		if err != nil {
			return err
		}
		mbox, err = state.waitevent(c)
		if err != nil {
			return err
		}
	}
}

func (state *stateT) eventpoll(c *client.Client, mbox *imap.MailboxStatus) error {
	from := uint32(1)
	to := mbox.Messages

	if to == 0 {
		return nil
	}

	seqset := new(imap.SeqSet)
	seqset.AddRange(from, to)

	messages := make(chan *imap.Message, 10)
	done := make(chan error, 1)

	var section imap.BodySectionName
	items := []imap.FetchItem{section.FetchItem()}

	go func() {
		err := c.Fetch(seqset, items, messages)
		done <- err
	}()

	date := time.Now().Format(time.RFC3339)

	for msg := range messages {
		m := &Message{
			Date: date,
			Header: Header{
				Date: date,
			},
		}

		if state.verbose > 1 {
			log.Printf("message: %+v\n", msg)
		}

		r := msg.GetBody(&section)
		if r == nil {
			if state.verbose > 1 {
				log.Printf("Server didn't return message body: %+v", msg)
			}
			continue
		}

		mr, err := mail.CreateReader(r)
		if err != nil {
			return err
		}

		if date, err := mr.Header.Date(); err == nil {
			m.Header.Date = date.Local().Format(time.RFC3339)
		}

		if from, err := mr.Header.AddressList("From"); err == nil {
			m.Header.From = addressList(from)
		}

		if to, err := mr.Header.AddressList("To"); err == nil {
			m.Header.To = addressList(to)
		}

		if subject, err := mr.Header.Subject(); err == nil {
			m.Header.Subject = subject
		}

		m.Header.Map = mr.Header.Map()

		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			} else if err != nil {
				return err
			}

			switch h := p.Header.(type) {
			case *mail.InlineHeader:
				b, err := io.ReadAll(p.Body)
				if err != nil {
					log.Println(err)
				}
				m.Body = append(m.Body, string(b))
			case *mail.AttachmentHeader:
				filename, err := h.Filename()
				if err != nil {
					log.Println(err)
				}
				m.Attachment = append(m.Attachment, Attachment{Name: filename})
			}
		}

		if err := state.output(m); err != nil {
			log.Println(err)
		}
	}

	if err := <-done; err != nil {
		return err
	}

	item := imap.FormatFlagsOp(imap.AddFlags, true)
	flags := []interface{}{imap.DeletedFlag}
	if err := c.Store(seqset, item, flags, nil); err != nil {
		return err
	}

	return c.Expunge(nil)
}

func addressList(al []*mail.Address) []string {
	a := make([]string, 0, len(al))
	for _, v := range al {
		a = append(a, v.String())
	}
	return a
}

func (state *stateT) waitevent(c *client.Client) (*imap.MailboxStatus, error) {
	idleClient := idle.NewClient(c)

	updates := make(chan client.Update, 64)
	c.Updates = updates

	done := make(chan error, 1)
	stop := make(chan struct{})
	go func() {
		err := idleClient.IdleWithFallback(stop, state.pollTimeout)
		done <- err
	}()

	for {
		select {
		case update := <-updates:
			switch v := update.(type) {
			case *client.MailboxUpdate:
				if state.verbose > 1 {
					log.Printf("%+v", v.Mailbox)
				}
				close(stop)
				<-done
				return v.Mailbox, nil
			default:
			}
		case err := <-done:
			if err == nil {
				err = errEOF
			}
			return nil, err
		}
	}
}

func (state *stateT) output(m *Message) error {
	funcMap := template.FuncMap{
		"re": func(s string, r string) bool {
			re := regexp.MustCompile(r)
			return re.MatchString(s)
		},
		"join": func(sep string, s []string) string { return strings.Join(s, sep) },
		"strip": func(s string) string {
			return html.UnescapeString(bluemonday.StrictPolicy().Sanitize(s))
		},
	}

	tmpl, err := template.New("message").Funcs(funcMap).Parse(state.template)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, m); err != nil {
		return err
	}

	if _, err := os.Stdout.Write(buf.Bytes()); err != nil {
		return err
	}

	return nil
}
