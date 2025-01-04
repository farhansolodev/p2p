package main

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

var peers = make(map[string]string)

func listenForMessages(conn *net.UDPConn, wg *sync.WaitGroup) {
    defer wg.Done()
    
    buffer := make([]byte, 1024)
    for {
        n, addr, err := conn.ReadFromUDP(buffer)
        if err != nil {
            fmt.Printf("Error receiving data: %v\n", err)
            continue
        }
        
        // Don't print ping messages
        if string(buffer[:n]) != "ping" {
            fmt.Printf("(IP: %s, Port: %d)> %s\n", 
                addr.IP.String(), 
                addr.Port, 
                string(buffer[:n]))
        }
    }
}

func sendPings(conn *net.UDPConn, remoteAddr *net.UDPAddr, wg *sync.WaitGroup) {
    defer wg.Done()
    
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()
    
    for {
        <-ticker.C
        _, err := conn.WriteToUDP([]byte("ping"), remoteAddr)
        if err != nil {
            // Silently continue on ping errors
            continue
        }
    }
}

func main() {
    // Define flags
    localPort := flag.Int("lport", 0, "Local port to bind to")
    remoteIP := flag.String("rip", "", "Remote IP address")
    remotePort := flag.Int("rport", 0, "Remote port")

    // Parse flags
    flag.Parse()

    // Validate flags
    if *localPort == 0 || *remoteIP == "" || *remotePort == 0 {
        fmt.Println("Error: All flags are required")
        fmt.Println("Usage:")
        flag.PrintDefaults()
        os.Exit(1)
    }

    // Create a UDP address for the local endpoint
    localAddr := &net.UDPAddr{
        IP:   net.ParseIP("0.0.0.0"),
        Port: *localPort,
    }
    
    // Create a UDP connection bound to the specific interface and port
    conn, err := net.ListenUDP("udp", localAddr)
    if err != nil {
        fmt.Printf("Failed to bind to port %d: %v\n", *localPort, err)
        os.Exit(1)
    }
    defer conn.Close()
    
    fmt.Printf("Successfully bound to port %d\n", *localPort)
    
    // Define the remote endpoint
    remoteAddr := &net.UDPAddr{
        IP:   net.ParseIP(*remoteIP),
        Port: *remotePort,
    }

    // Validate remote IP
    if remoteAddr.IP == nil {
        fmt.Printf("Invalid remote IP address: %s\n", *remoteIP)
        os.Exit(1)
    }
    
    // Set up wait group for goroutine synchronization
    var wg sync.WaitGroup
    wg.Add(2) // Added one more for the ping goroutine
    
    // Start the listener goroutine
    go listenForMessages(conn, &wg)
    
    // Start the ping goroutine
    go sendPings(conn, remoteAddr, &wg)
    
    // Create a scanner for reading console input
    scanner := bufio.NewScanner(os.Stdin)
    fmt.Printf("Connected to %s:%d\n", *remoteIP, *remotePort)
    fmt.Println("Type your message and press Enter. Type 'quit' to exit.")
    
    // Main loop for sending messages
    for {
        if !scanner.Scan() {
            break
        }
        
        message := scanner.Text()
        if message == "quit" {
            break
        }
        
        // Send the message
        _, err = conn.WriteToUDP([]byte(message), remoteAddr)
        if err != nil {
            fmt.Printf("Failed to send data: %v\n", err)
            continue
        }
    }
    
    fmt.Println("Shutting down...")
}