//go:build linux

// Package secret is a minimal freedesktop Secret Service (D-Bus) client matching how the Rust app
// stores keyring items: attributes {application: "rustxterm", id} in the default collection. It is
// used to unlock the store's keys.v1 envelope keyring wrap silently (migrate-gui-to-go-wails 2.2).
package secret

import (
	"errors"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	busName     = "org.freedesktop.secrets"
	servicePath = "/org/freedesktop/secrets"
	defaultColl = "/org/freedesktop/secrets/aliases/default"
	application = "rustxterm"
	connTimeout = 3 * time.Second
)

// Service talks to the session Secret Service. A missing or broken service makes every call return
// ErrUnavailable, never a hang.
type Service struct{}

// New returns a Secret Service client.
func New() *Service { return &Service{} }

// ErrUnavailable means the Secret Service could not be reached (no session bus, no daemon).
var ErrUnavailable = errors.New("secret service unavailable")

func attrs(id string) map[string]string {
	return map[string]string{"application": application, "id": id}
}

type secretValue struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

type conn struct {
	bus     *dbus.Conn
	service dbus.BusObject
	session dbus.ObjectPath
}

func connect() (*conn, error) {
	bus, err := dbus.SessionBus()
	if err != nil {
		return nil, ErrUnavailable
	}
	svc := bus.Object(busName, servicePath)
	var output dbus.Variant
	var session dbus.ObjectPath
	call := svc.Call("org.freedesktop.Secret.Service.OpenSession", 0, "plain", dbus.MakeVariant(""))
	if call.Err != nil {
		bus.Close()
		return nil, ErrUnavailable
	}
	if err := call.Store(&output, &session); err != nil {
		bus.Close()
		return nil, ErrUnavailable
	}
	return &conn{bus: bus, service: svc, session: session}, nil
}

func (c *conn) close() { c.bus.Close() }

// unlock makes a best effort to unlock the default collection (non-interactive); a prompt is ignored.
func (c *conn) unlock() {
	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	_ = c.service.Call("org.freedesktop.Secret.Service.Unlock", 0,
		[]dbus.ObjectPath{dbus.ObjectPath(defaultColl)}).Store(&unlocked, &prompt)
}

func (c *conn) search(id string) (unlocked, locked []dbus.ObjectPath, err error) {
	err = c.service.Call("org.freedesktop.Secret.Service.SearchItems", 0, attrs(id)).Store(&unlocked, &locked)
	return
}

// Get returns the secret for id; ok=false when absent, ErrUnavailable when the keyring cannot be reached.
func (s *Service) Get(id string) (secret []byte, ok bool, err error) {
	c, err := connect()
	if err != nil {
		return nil, false, err
	}
	defer c.close()
	unlocked, locked, err := c.search(id)
	if err != nil {
		return nil, false, ErrUnavailable
	}
	item := dbus.ObjectPath("")
	if len(unlocked) > 0 {
		item = unlocked[0]
	} else if len(locked) > 0 {
		c.unlock()
		if u, _, e := c.search(id); e == nil && len(u) > 0 {
			item = u[0]
		}
	}
	if item == "" {
		return nil, false, nil
	}
	var sv secretValue
	if err := c.bus.Object(busName, item).Call("org.freedesktop.Secret.Item.GetSecret", 0, c.session).Store(&sv); err != nil {
		return nil, false, nil
	}
	return sv.Value, true, nil
}

// Set stores secret under id, replacing any existing item.
func (s *Service) Set(id string, secret []byte) error {
	c, err := connect()
	if err != nil {
		return err
	}
	defer c.close()
	c.unlock()
	props := map[string]dbus.Variant{
		"org.freedesktop.Secret.Item.Label":      dbus.MakeVariant("rustXterm database key (" + id + ")"),
		"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(attrs(id)),
	}
	sv := secretValue{Session: c.session, Value: secret, ContentType: "application/octet-stream"}
	var item, prompt dbus.ObjectPath
	call := c.bus.Object(busName, dbus.ObjectPath(defaultColl)).
		Call("org.freedesktop.Secret.Collection.CreateItem", 0, props, sv, true)
	if call.Err != nil {
		return ErrUnavailable
	}
	return call.Store(&item, &prompt)
}

// Delete removes every item stored under id.
func (s *Service) Delete(id string) error {
	c, err := connect()
	if err != nil {
		return err
	}
	defer c.close()
	unlocked, locked, err := c.search(id)
	if err != nil {
		return ErrUnavailable
	}
	for _, item := range append(unlocked, locked...) {
		var prompt dbus.ObjectPath
		_ = c.bus.Object(busName, item).Call("org.freedesktop.Secret.Item.Delete", 0).Store(&prompt)
	}
	return nil
}
