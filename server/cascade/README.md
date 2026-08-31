# cascade/facefinder

`facefinder` is the binary face-detection cascade shipped with
[github.com/esimov/pigo](https://github.com/esimov/pigo) (`cascade/facefinder`,
v1.4.6), copied here verbatim. It is a packed set of classification binary
trees; `pigo.NewPigo().Unpack()` parses it and `RunCascade` evaluates it.

It lives in the repository rather than being fetched at runtime because
`bestcrop.go` embeds it into the server binary with `//go:embed`, so the frame's
cropper has no runtime file or network dependency at all.

License: MIT, © Endre Simo — the same license as pigo itself, which is already a
module dependency of the server.
