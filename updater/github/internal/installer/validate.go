package installer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// checksumLineParts is the number of whitespace-separated fields in a checksums
// file line: the hash and the filename.
const checksumLineParts = 2

// Validator represents an interface which enables additional validation of releases.
type Validator interface {
	// Validate validates release bytes against an additional asset bytes.
	// See SHAValidator for more information.
	Validate(filename string, release, asset []byte) error
	// GetValidationAssetName returns the additional asset name containing the validation checksum.
	// The asset containing the checksum can be based on the release asset name
	// Please note if the validation file cannot be found, the DetectLatest and DetectVersion methods
	// will fail with a wrapped ErrValidationAssetNotFound error
	GetValidationAssetName(releaseFilename string) string
}

// SHAValidator specifies a SHA256 validator for additional file validation
// before updating.
type SHAValidator struct{}

// Validate checks the SHA256 sum of the release against the contents of an
// additional asset file.
func (v *SHAValidator) Validate(_ string, release, asset []byte) error {
	// we'd better check the size of the file otherwise it's going to panic
	if len(asset) < sha256.BlockSize {
		return ErrIncorrectChecksumFile
	}

	hash := string(asset[:sha256.BlockSize])
	calculatedHash := fmt.Sprintf("%x", sha256.Sum256(release))

	if equal, err := hexStringEquals(sha256.Size, calculatedHash, hash); !equal {
		if err == nil {
			return fmt.Errorf(
				"expected %q, found %q: %w",
				hash,
				calculatedHash,
				ErrChecksumValidationFailed,
			)
		}
		return fmt.Errorf("%w: %w", err, ErrChecksumValidationFailed)
	}
	return nil
}

// GetValidationAssetName returns the asset name for SHA256 validation.
func (v *SHAValidator) GetValidationAssetName(releaseFilename string) string {
	return releaseFilename + ".sha256"
}

// ChecksumValidator is a SHA256 checksum validator where all the validation hash are in a single file (one per line)
type ChecksumValidator struct {
	// UniqueFilename is the name of the global file containing all the checksums
	// Usually "checksums.txt", "SHA256SUMS", etc.
	UniqueFilename string
}

// Validate the SHA256 sum of the release against the contents of an
// additional asset file containing all the checksums (one file per line).
func (v *ChecksumValidator) Validate(filename string, release, asset []byte) error {
	hash, err := findChecksum(filename, asset)
	if err != nil {
		return err
	}
	return new(SHAValidator).Validate(filename, release, []byte(hash))
}

func findChecksum(filename string, content []byte) (string, error) {
	// check if the file has windows line ending (probably better than just testing the platform)
	crlf := []byte("\r\n")
	lf := []byte("\n")
	eol := lf
	if bytes.Contains(content, crlf) {
		log.Print("Checksum file is using windows line ending")
		eol = crlf
	}
	lines := bytes.Split(content, eol)
	log.Printf("Checksum validator: %d checksums available, searching for %q", len(lines), filename)
	for _, line := range lines {
		// skip empty line
		if len(line) == 0 {
			continue
		}
		parts := bytes.Split(line, []byte("  "))
		if len(parts) != checksumLineParts {
			return "", ErrIncorrectChecksumFile
		}
		if string(parts[1]) == filename {
			return string(parts[0]), nil
		}
	}
	return "", ErrHashNotFound
}

// GetValidationAssetName returns the unique asset name for SHA256 validation.
func (v *ChecksumValidator) GetValidationAssetName(_ string) string {
	return v.UniqueFilename
}

func hexStringEquals(size int, a, b string) (bool, error) {
	size *= 2
	if len(a) != size || len(b) != size {
		return false, nil
	}
	bytesA, err := hex.DecodeString(a)
	if err != nil {
		return false, err
	}
	bytesB, err := hex.DecodeString(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(bytesA, bytesB), nil
}

// Verify interface
var (
	_ Validator = (*SHAValidator)(nil)
	_ Validator = (*ChecksumValidator)(nil)
)
