The server gives a list of resource packs the client is forced to download. The server sends them in "chunks" It sends a chunk to a client, then awaits until the client is done downloading said chunk and then sends another, by infinitely "Hanging" this process, and using multiple connections (On the same account) We can quickly exhaust and overwelm the server.

Requires a server with a resource pack installed larger than 2 chunks.

go run test.go ip:port connections
