package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func sendPings(conn *net.UDPConn, remoteAddr *net.UDPAddr) {
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

type Message struct {
    time time.Time
    ip string
    port int
    text string
}

type Model struct {
    sub chan Message // where we'll receive message notifications
    conn *net.UDPConn
    remoteAddr *net.UDPAddr
    localPort int
    peerMessages []Message
    userMessages []Message
    currentUserMessage Message
}

func sendMessage(conn *net.UDPConn, remoteAddr *net.UDPAddr, message string) tea.Cmd {
    return func() tea.Msg {
        _, _ = conn.WriteToUDP([]byte(message), remoteAddr)
        return nil
    }
}

func listenForMessages(sub chan Message, conn *net.UDPConn) tea.Cmd {
	return func() tea.Msg {
		buffer := make([]byte, 1024)
        for {
            n, addr, err := conn.ReadFromUDP(buffer)
            if err != nil {
                fmt.Printf("Error receiving data: %v\n", err)
                continue
            }
            
            if string(buffer[:n]) != "ping" {
                sub <- Message{
                    time: time.Now(),
                    ip:   addr.IP.String(),
                    port: addr.Port,
                    text: string(buffer[:n]),
                }
            }
        }
	}
}

type Response Message

// A command that waits for the messages on a channel.
func waitForMessages(sub chan Message) tea.Cmd {
	return func() tea.Msg {
		return Response(<-sub)
	}
}

func (m Model) Init() tea.Cmd {
    return tea.Batch(
        listenForMessages(m.sub, m.conn),
        waitForMessages(m.sub),
    )
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        switch msg.Type {
        case tea.KeyEnter:
            
            newMessage := Message{
                time: time.Now(),
                ip:   "localhost",
                port: m.localPort,
                text: m.currentUserMessage.text,
            }
            
            // Add to userMessages and clear current message
            m.userMessages = append(m.userMessages, newMessage)
            m.currentUserMessage.text = ""
            
            return m, sendMessage(m.conn, m.remoteAddr, newMessage.text)

        case tea.KeyCtrlC:
            return m, tea.Quit
            
        case tea.KeyBackspace:
            if len(m.currentUserMessage.text) > 0 {
                m.currentUserMessage.text = m.currentUserMessage.text[:len(m.currentUserMessage.text)-1]
            }
            return m, nil

        // Handle regular typing
        default:
            if msg.Alt { return m, nil }
            m.currentUserMessage.text += msg.String()
            return m, nil
        }

    // Handle incoming peer messages
    case Response:
        m.peerMessages = append(m.peerMessages, Message(msg))
        return m, waitForMessages(m.sub)

    // Handle any other events
    default:
        return m, nil
    }
}

func (m Model) View() string {
    allMessages := append(m.peerMessages, m.userMessages...)
    
    // Sort the combined slice by timestamp
	sort.Slice(allMessages, func(i, j int) bool {
		return allMessages[i].time.Before(allMessages[j].time)
	})
    
    // print every message like [timestamp] ip:port> text
    var output string
    for _, message := range allMessages {
        output += fmt.Sprintf("[%s] %s:%d> %s\n", message.time.Format("15:04:05"), message.ip, message.port, message.text)
    }

    output += fmt.Sprintf("\n> %s", m.currentUserMessage.text)

    return output
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
    
    // fmt.Printf("Successfully bound to port %d\n", *localPort)
    
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
    
    // Start the ping goroutine
    go sendPings(conn, remoteAddr)

    if _, err := tea.NewProgram(Model{
        localPort: *localPort,
        conn: conn,
        remoteAddr: remoteAddr,
        sub: make(chan Message),
        peerMessages: []Message{},
        userMessages: []Message{},
        currentUserMessage: Message{
            text: "",
        },
    }).Run(); err != nil {
        fmt.Printf("Uh oh, there was an error: %v\n", err)
        os.Exit(1)
    }
}