package main

import (
    "bytes"
    "fmt"
    "net"
    "os"
    "strconv"
    "sync"
    "time"

    "github.com/go-jose/go-jose/v4/json"
    "github.com/sandertv/gophertunnel/minecraft"
    "github.com/sandertv/gophertunnel/minecraft/auth"
    "github.com/sandertv/gophertunnel/minecraft/protocol"
    "github.com/sandertv/gophertunnel/minecraft/protocol/packet"
    "golang.org/x/oauth2"
)

var (
    mcConnections     []*minecraft.Conn
    connectionsMutex  sync.Mutex
)

func main() {
    if len(os.Args) < 2 {
        fmt.Println("Usage: go run test.go <server_address> [number_of_connections]")
        fmt.Println("Example: go run test.go play.galaxite.net:19132 5")
        return
    }
    
    serverAddress := os.Args[1]
    times := 1
    
    if len(os.Args) > 2 {
        var err error
        times, err = strconv.Atoi(os.Args[2])
        if err != nil {
            fmt.Printf("Invalid number of connections: %v\n", err)
            fmt.Println("Usage: go run test.go <server_address> [number_of_connections]")
            return
        }
        if times <= 0 {
            fmt.Println("Number of connections must be greater than 0")
            return
        }
    }

    fmt.Printf("Target server: %s\n", serverAddress)
    fmt.Printf("Creating %d concurrent connection(s)...\n", times)

    var wg sync.WaitGroup
    for i := 1; i <= times; i++ {
        wg.Add(1)
        go func(connID int) {
            defer wg.Done()
            
            delay := time.Duration(connID-1) * 10 * time.Second
            if delay > 0 {
                fmt.Printf("Connection %d: Waiting %v before starting...\n", connID, delay)
                time.Sleep(delay)
            }
            
            fmt.Printf("Starting connection %d\n", connID)
            if err := runConnection(connID, serverAddress); err != nil {
                fmt.Printf("Connection %d failed: %v\n", connID, err)
            }
        }(i)
    }
    
    fmt.Println("All connections scheduled. Press Ctrl+C to exit.")
    wg.Wait()
}

func runConnection(connID int, serverAddress string) error {
    fmt.Printf("Connection %d: Connecting to %s...\n", connID, serverAddress)
    
    var firstChunkHandled bool
    var mcConn *minecraft.Conn
    
    conn, err := minecraft.Dialer{
        TokenSource: tokenSourceTest(),
        PacketFunc: func(header packet.Header, payload []byte, src, dst net.Addr) {
            pool := packet.NewServerPool()
            pkFunc, ok := pool[header.PacketID]
            if !ok {
                return
            }
            pkt := pkFunc()
            reader := bytes.NewReader(payload)
            func() { defer func() { recover() }(); pkt.Marshal(protocol.NewReader(reader, 0, true)) }()
            handlePacket(pkt, mcConn, &firstChunkHandled, connID)
        },
        DisconnectOnInvalidPackets: false,
        DisconnectOnUnknownPackets: false,
    }.DialTimeout("raknet", serverAddress, time.Minute)
    if err != nil {
        return fmt.Errorf("connect failed: %v", err)
    }
    mcConn = conn
    
    connectionsMutex.Lock()
    mcConnections = append(mcConnections, conn)
    connectionsMutex.Unlock()
    
    defer func() {
        conn.Close()
        connectionsMutex.Lock()
        for i, c := range mcConnections {
            if c == conn {
                mcConnections = append(mcConnections[:i], mcConnections[i+1:]...)
                break
            }
        }
        connectionsMutex.Unlock()
    }()

    if err := conn.DoSpawn(); err != nil {
        return fmt.Errorf("spawn failed: %v", err)
    }
    fmt.Printf("Connection %d: Spawned. Waiting for resource pack info...\n", connID)

    for !firstChunkHandled {
        if _, err := mcConn.ReadPacket(); err != nil {
            return fmt.Errorf("connection closed: %v", err)
        }
    }

    fmt.Printf("Connection %d: Hanging after first chunk.\n", connID)
    select {}
}

func handlePacket(pkt packet.Packet, mcConn *minecraft.Conn, firstChunkHandled *bool, connID int) {
    switch p := pkt.(type) {
    case *packet.ResourcePacksInfo:
        var list []string
        for _, tp := range p.TexturePacks {
            list = append(list, tp.UUID.String()+"_"+tp.Version)
        }
        if mcConn != nil {
            _ = mcConn.WritePacket(&packet.ResourcePackClientResponse{Response: packet.PackResponseSendPacks, PacksToDownload: list})
        }
        go func() {
            time.Sleep(150 * time.Millisecond)
            if mcConn != nil {
                _ = mcConn.WritePacket(&packet.ResourcePackClientResponse{Response: packet.PackResponseAllPacksDownloaded})
            }
        }()
        fmt.Printf("Connection %d: Requested %d pack(s); will stop after first chunk.\n", connID, len(list))

    case *packet.ResourcePackDataInfo:
        fmt.Printf("Connection %d: Pack: %s size=%d bytes chunks=%d\n", connID, p.UUID, p.Size, p.ChunkCount)

    case *packet.ResourcePackChunkData:
        if *firstChunkHandled {
            return
        }
        *firstChunkHandled = true
        fmt.Printf("Connection %d: First chunk: pack=%s offset=%d len=%d (no further responses)\n", connID, p.UUID, p.DataOffset, len(p.Data))
    }
}

func tokenSourceTest() oauth2.TokenSource {
    token := new(oauth2.Token)
    if tokenData, err := os.ReadFile("token.tok"); err == nil {
        _ = json.Unmarshal(tokenData, token)
    } else {
        fmt.Println("Auth required...")
        newToken, err := auth.RequestLiveToken()
        if err != nil {
            fmt.Printf("Auth failed: %v\n", err)
            os.Exit(1)
        }
        token = newToken
    }
    src := auth.RefreshTokenSource(token)
    if _, err := src.Token(); err != nil {
        fmt.Println("Re-auth...")
        newToken, err := auth.RequestLiveToken()
        if err != nil {
            fmt.Printf("Re-auth failed: %v\n", err)
            os.Exit(1)
        }
        token = newToken
        src = auth.RefreshTokenSource(token)
    }
    return src
}
