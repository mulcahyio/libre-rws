# RWS fork of gonetdicom (base: upstream v0.17.0, MIT)

Fork maintained by Patrick Mulcahy, Mulcahy.io (dev@mulcahy.io).
Upstream: github.com/godicom-dev/gonetdicom, Copyright (c) 2026 godicom-dev,
MIT License (see LICENSE in this directory, unchanged).

Changes carried in this fork (each should be offered upstream as a PR):

1. `ServerConfig.SelectTransferSyntax` - lets the acceptor choose the transfer
   syntax per presentation context. Upstream always prefers Implicit VR Little
   Endian when it is proposed, which forces modalities to decompress (and makes
   lossy-JPEG senders fail).

Planned:

2. Streaming C-MOVE/C-GET plans (iterator instead of `[]StoreRequest`) with
   Pending responses and C-CANCEL support.
3. C-GET: pass the negotiated transfer syntax to the handler; count failed
   C-STORE sub-operations.
4. Expose the calling AE title and peer address to handlers via context.
5. Optional lazy decode of inbound C-STORE datasets (avoid holding raw + decoded).
