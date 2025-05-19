# fec-test

To run this:
- Server, `go run .` it listen to 8080
- Client, open https://vaporplay-client.3drx.top, fill in server url (for example http://<server-ip>:8080). Other options doesn't matter, click next and select the only "game" and connect.

The problem:
- When using FEC and NACK at the same time, by default the nack packets gets fec encoded, which is not how it's expected to work. This is fixed in this repo's interceptor/flexfec.
- When calling `NewFlexEncoder03` inside encoder_interceptor.go, we should do this
```go
r.flexFecEncoder = NewFlexEncoder03(info.PayloadTypeForwardErrorCorrection, info.SSRCForwardErrorCorrection)
```
instead of this
```go
r.flexFecEncoder = NewFlexEncoder03(info.PayloadType, info.SSRC)
```
This is also fixed in this repo.
- When using the TWCC header extension interceptor together with NACK and FEC interceptor, the packet's data smh gets corrupted by the TWCC header extension interceptor. This corruption in packet data will also cause video corruption.

Thanks to https://github.com/aalekseevx, problems in the fec codec itself nolonger exist. It can now recover around 3% packet loss with current setting (2 FEC packets each 5 data packets) with absolute no frame rate drop and no delay introduced.
