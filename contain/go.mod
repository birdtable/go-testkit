module github.com/birdtable/go-testkit/contain

go 1.27.1

toolchain go1.27.4

require (
	github.com/birdtable/go-testkit v0.4.1-0.20261126014529-ebf77cb0e26a
	kernel.org/pub/linux/libs/security/libcap/cap v1.2.78
)

require kernel.org/pub/linux/libs/security/libcap/psx v1.2.78 // indirect

retract v0.1.1 // requires a go-testkit version that cannot be fetched
