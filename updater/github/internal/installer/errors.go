package installer

import "errors"

// Possible errors returned
var (
	ErrInvalidSlug                 = errors.New("invalid slug format, expected 'owner/name'")
	ErrIncorrectParameterOwner     = errors.New("incorrect parameter \"owner\"")
	ErrIncorrectParameterRepo      = errors.New("incorrect parameter \"repo\"")
	ErrInvalidRelease              = errors.New("invalid release (nil argument)")
	ErrValidationAssetNotFound     = errors.New("validation file not found")
	ErrIncorrectChecksumFile       = errors.New("incorrect checksum file format")
	ErrChecksumValidationFailed    = errors.New("sha256 validation failed")
	ErrHashNotFound                = errors.New("hash not found in checksum file")
	ErrCannotDecompressFile        = errors.New("failed to decompress")
	ErrExecutableNotFoundInArchive = errors.New("executable not found")
)
