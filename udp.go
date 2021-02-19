package main

import (
	"PortForwardGo/zlog"
	"errors"
	"net"
	"time"
)

type UDPDistribute struct {
	Connected bool
	Conn      *(net.UDPConn)
	Cache     chan []byte
	RAddr     net.Addr
	LAddr     net.Addr
}

func NewUDPDistribute(conn *(net.UDPConn), addr net.Addr) *UDPDistribute {
	return &UDPDistribute{
		Connected: true,
		Conn:      conn,
		Cache:     make(chan []byte, 16),
		RAddr:     addr,
		LAddr:     conn.LocalAddr(),
	}
}

func (this *UDPDistribute) Close() error {
	this.Connected = false
	return nil
}

func (this *UDPDistribute) Read(b []byte) (n int, err error) {
	if !this.Connected {
		return 0, errors.New("udp conn has closed")
	}

	select {
	case <-time.After(16 * time.Second):
		return 0, errors.New("i/o read timeout")
	case data := <-this.Cache:
		n := len(data)
		copy(b, data)
		return n, nil
	}
}

func (this *UDPDistribute) Write(b []byte) (int, error) {
	if !this.Connected {
		return 0, errors.New("udp conn has closed")
	}
	return this.Conn.WriteTo(b, this.RAddr)
}

func (this *UDPDistribute) RemoteAddr() net.Addr {
	return this.RAddr
}

func (this *UDPDistribute) LocalAddr() net.Addr {
	return this.LAddr
}

func (this *UDPDistribute) SetDeadline(t time.Time) error {
	return this.Conn.SetDeadline(t)
}

func (this *UDPDistribute) SetReadDeadline(t time.Time) error {
	return this.Conn.SetReadDeadline(t)
}

func (this *UDPDistribute) SetWriteDeadline(t time.Time) error {
	return this.Conn.SetWriteDeadline(t)
}

func LoadUDPRules(i string) {

	Setting.Rules.RLock()
	address, _ := net.ResolveUDPAddr("udp", ":"+Setting.Config.Rules[i].Listen)
	ln, err := net.ListenUDP("udp", address)

	if err == nil {
		zlog.Info("Loaded [", i, "] (UDP)", Setting.Config.Rules[i].Listen, " => ", Setting.Config.Rules[i].Forward)
	} else {
		Setting.Rules.RUnlock()
		zlog.Error("Load failed [", i, "] (UDP) Error: ", err)
		SendListenError(i)
		return
	}

	Setting.Listener.UDP[i] = ln
	Setting.Rules.RUnlock()

	go AcceptUDP(ln, i)
}

func DeleteUDPRules(i string) {
	if _, ok := Setting.Listener.UDP[i]; ok {
		err := Setting.Listener.UDP[i].Close()
		for err != nil {
			time.Sleep(time.Second)
			err = Setting.Listener.UDP[i].Close()
		}
		delete(Setting.Listener.UDP, i)
	}
	Setting.Rules.Lock()
	delete(Setting.Config.Rules, i)
	Setting.Rules.Unlock()
}

func AcceptUDP(serv *net.UDPConn, index string) {

	table := make(map[string]*UDPDistribute)
	for {
		buf := make([]byte, 32*1024)

		n, addr, err := serv.ReadFrom(buf)
		if err != nil {
			if err, ok := err.(net.Error); ok && err.Temporary() {
				continue
			}
			break
		}

		go func() {
			buf = buf[:n]

			if d, ok := table[addr.String()]; ok {
				if d.Connected {
					d.Cache <- buf
					return
				} else {
					delete(table, addr.String())
				}
			}

			Setting.Rules.RLock()
			rule := Setting.Config.Rules[index]
			Setting.Rules.RUnlock()

			if rule.Status != "Active" && rule.Status != "Created" {
				return
			}

			conn := NewUDPDistribute(serv, addr)
			table[addr.String()] = conn
			conn.Cache <- buf
			go udp_handleRequest(conn, index, rule)
		}()
	}
}

func ConnUDP(address string) (net.Conn, error) {
	conn, err := net.DialTimeout("udp", address, 10*time.Second)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func udp_handleRequest(conn net.Conn, index string, r Rule) {
	proxy, err := ConnUDP(r.Forward)
	if err != nil {
		conn.Close()
		return
	}

	go copyIO(conn, proxy, r.UserID)
	go copyIO(proxy, conn, r.UserID)
}
