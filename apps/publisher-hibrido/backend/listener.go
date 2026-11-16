package main

import "net"

func netListen() (net.Listener, error) { return net.Listen("tcp", backendAddr) }
