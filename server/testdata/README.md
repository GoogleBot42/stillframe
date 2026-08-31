# sample.jpg

`sample.jpg` is the 320x400 test photo shipped with
[github.com/esimov/pigo](https://github.com/esimov/pigo) as `testdata/sample.jpg`
(v1.4.6), copied here verbatim.

It is used only by the Go tests, as a picture with a face the embedded cascade
is known to find — a synthetic image cannot exercise the detector at all. It is
not part of the server binary and is never served.

License: MIT, © Endre Simo — the same license as pigo itself, which is already a
module dependency of the server.
