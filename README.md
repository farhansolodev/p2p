# Signaling implementation

- Over WS, server receives both peers' IPs, recipient IPs & UDP ports to bind to.
- Server has EIF-NAT, so can receive UDP packets from any remote address:port.
  Receives UDP hole punch from Peer A's IP. Obtains their remote port.
  Receives UDP hole punch from Peer B's IP. Obtains their remote port.
- Over WS, server sends Peer A's remote port to Peer B & vice-versa.
- Both peers have EIM-NAT so punching another hole from the same source port, this time to the other peer, will reuse the same mapping (internal->external source port). The purpose of this hole is to allow filtering on the other peer's IP:port.
- (Optional) Handshake between peers to confirm connection:
  1. Peer A sends hole punch, doesn't go through.
  2. Peer B sends hole punch, goes through.
  3. Peer A sends hole punch, goes through.
  4. Peer A sends handshake packet and waits for response.
  5. Peer B sends hole punch, goes through but gets ignored.
  6. Peer B sends handshake packet and waits for response.
  7. Peer B tells user that Peer A is connected if it receives handshake packet before timeout.
  8. Peer A tells user that Peer B is connected if it receives handshake packet before timeout.
  9. Keep repeating steps 7 & 8 and tell the user that the other peer is disconnected if the timeout is reached.
