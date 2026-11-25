//go:build cgo

package contain

// linksCgo is true: this test binary was built with cgo, as a -race build is, and links the C of libcap/psx.
const linksCgo = true
