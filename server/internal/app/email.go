package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/mail"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

const MaxAttachmentBytes = 20 << 20

type MailAttachment struct {
	Name, ContentType string
	Data              []byte
}
type OutgoingMail struct {
	To                  []string
	Subject, Text, HTML string
	Attachments         []MailAttachment
}

func BuildEmail(from string, m OutgoingMail) ([]byte, error) {
	if !validEmail(from) || len(m.To) == 0 || len(m.To) > 20 || strings.ContainsAny(m.Subject, "\r\n") || len(m.Subject) > 500 || m.Text == "" {
		return nil, errors.New("Invalid email headers or missing plain-text body")
	}
	recipients := []string{}
	for _, to := range m.To {
		if !validEmail(to) {
			return nil, errors.New("Invalid email recipient")
		}
		recipients = append(recipients, (&mail.Address{Address: to}).String())
	}
	total := 0
	for _, a := range m.Attachments {
		total += len(a.Data)
		typ, _, err := mime.ParseMediaType(a.ContentType)
		if err != nil || !strings.Contains(typ, "/") || strings.ContainsAny(a.ContentType, "\r\n\x00") || a.Name == "" || len(a.Name) > 255 || strings.ContainsAny(a.Name, "\r\n/\\\x00") || total > MaxAttachmentBytes {
			return nil, errors.New("Invalid attachment or attachments exceed 20 MiB")
		}
	}
	if len(m.Text)+len(m.HTML) > 1<<20 {
		return nil, errors.New("Email body exceeds 1 MiB")
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@location-keeper.invalid>\r\nMIME-Version: 1.0\r\n", (&mail.Address{Name: "Location Keeper", Address: from}).String(), strings.Join(recipients, ", "), mime.QEncoding.Encode("utf-8", m.Subject), time.Now().UTC().Format(time.RFC1123Z), randomToken())
	var body bytes.Buffer
	mixed := multipart.NewWriter(&body)
	fmt.Fprintf(&out, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", mixed.Boundary())
	var alternative bytes.Buffer
	alt := multipart.NewWriter(&alternative)
	add := func(w *multipart.Writer, headers textproto.MIMEHeader, data []byte) error {
		part, err := w.CreatePart(headers)
		if err != nil {
			return err
		}
		encoded := base64.StdEncoding.EncodeToString(data)
		for len(encoded) > 76 {
			if _, err = io.WriteString(part, encoded[:76]+"\r\n"); err != nil {
				return err
			}
			encoded = encoded[76:]
		}
		_, err = io.WriteString(part, encoded+"\r\n")
		return err
	}
	for _, part := range []struct{ typ, value string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		if part.value == "" {
			continue
		}
		h := textproto.MIMEHeader{"Content-Type": {part.typ + "; charset=utf-8"}, "Content-Transfer-Encoding": {"base64"}}
		if err := add(alt, h, []byte(part.value)); err != nil {
			return nil, err
		}
	}
	if err := alt.Close(); err != nil {
		return nil, err
	}
	p, err := mixed.CreatePart(textproto.MIMEHeader{"Content-Type": {mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": alt.Boundary()})}})
	if err != nil {
		return nil, err
	}
	if _, err = p.Write(alternative.Bytes()); err != nil {
		return nil, err
	}
	for _, a := range m.Attachments {
		h := textproto.MIMEHeader{"Content-Type": {a.ContentType}, "Content-Disposition": {mime.FormatMediaType("attachment", map[string]string{"filename": a.Name})}, "Content-Transfer-Encoding": {"base64"}}
		if err = add(mixed, h, a.Data); err != nil {
			return nil, err
		}
	}
	if err = mixed.Close(); err != nil {
		return nil, err
	}
	out.Write(body.Bytes())
	return out.Bytes(), nil
}

func sendGmail(ctx context.Context, client *http.Client, access string, raw []byte) (string, error) {
	body, _ := json.Marshal(map[string]string{"raw": base64.RawURLEncoding.EncodeToString(raw)})
	req, err := http.NewRequestWithContext(ctx, "POST", "https://gmail.googleapis.com/gmail/v1/users/me/messages/send", bytes.NewReader(body))
	if err != nil {
		return "", errors.New("Could not create Gmail request")
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("Email delivery is unconfirmed; do not automatically retry")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return "", errors.New("Gmail rejected sender authorization; reconnect or check account permissions")
	}
	if resp.StatusCode == 429 {
		return "", errors.New("Gmail sending quota was reached")
	}
	if resp.StatusCode >= 500 {
		return "", errors.New("Email delivery is unconfirmed; do not automatically retry")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", errors.New("Gmail rejected the message")
	}
	var result struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&result) != nil || result.ID == "" {
		return "", errors.New("Email delivery is unconfirmed; do not automatically retry")
	}
	return result.ID, nil
}

type AlertCard struct{ Title, Message, Device, Area, When, AppURL string }

var alertCard = template.Must(template.New("alert").Parse(`<!doctype html><html><body style="margin:0;background:#f5f7f2;font-family:Arial,sans-serif;color:#204b3c"><table role="presentation" width="100%"><tr><td align="center" style="padding:24px"><table role="presentation" width="100%" style="max-width:560px;background:white;border:1px solid #dce5d7;border-radius:12px"><tr><td style="padding:28px"><p style="font-size:12px;letter-spacing:2px">LOCATION KEEPER</p><h1 style="font-size:24px">{{.Title}}</h1><p>{{.Message}}</p><p><strong>Device:</strong> {{.Device}}<br><strong>Area:</strong> {{.Area}}<br><strong>Recorded:</strong> {{.When}}</p><p style="padding-top:16px"><a href="{{.AppURL}}" style="display:inline-block;padding:12px 18px;background:#204b3c;color:white;text-decoration:none;border-radius:6px">View activity</a></p><p style="font-size:12px;color:#66755e">Based on reported observations. Open the app for current details.</p></td></tr></table></td></tr></table></body></html>`))

func RenderAlertCard(c AlertCard) (text, html string, err error) {
	u, e := url.Parse(c.AppURL)
	if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", errors.New("Invalid activity link")
	}
	var out bytes.Buffer
	if err = alertCard.Execute(&out, c); err != nil {
		return "", "", err
	}
	text = fmt.Sprintf("%s\n\n%s\n\nDevice: %s\nArea: %s\nRecorded: %s\n\nView activity: %s", c.Title, c.Message, c.Device, c.Area, c.When, c.AppURL)
	return text, out.String(), nil
}
