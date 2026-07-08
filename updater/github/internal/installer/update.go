package installer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
)

// UpdateTo downloads an executable from the source provider and replace current binary with the downloaded one.
// It downloads a release asset via the source provider so this function is available for update releases on private repository.
func (up *Updater) UpdateTo(ctx context.Context, rel *Release, cmdPath string) error {
	if rel == nil {
		return ErrInvalidRelease
	}

	data, err := up.download(ctx, rel, rel.AssetID)
	if err != nil {
		return fmt.Errorf("failed to read asset %q: %w", rel.AssetName, err)
	}

	if up.validator != nil {
		err = up.validate(ctx, rel, data)
		if err != nil {
			return err
		}
	}

	return up.decompressAndUpdate(bytes.NewReader(data), rel.AssetName, rel.AssetURL, cmdPath)
}

func (up *Updater) decompressAndUpdate(src io.Reader, assetName, assetURL, cmdPath string) error {
	_, cmd := filepath.Split(cmdPath)
	asset, err := DecompressCommand(src, assetName, cmd, up.os, up.arch)
	if err != nil {
		return err
	}

	log.Printf("Will update %s to the latest downloaded from %s", cmdPath, assetURL)
	return Apply(asset, Options{
		TargetPath:  cmdPath,
		OldSavePath: up.oldSavePath,
	})
}

// validate loads the validation file and passes it to the validator.
// The validation is successful if no error was returned
func (up *Updater) validate(ctx context.Context, rel *Release, data []byte) error {
	if rel == nil {
		return ErrInvalidRelease
	}

	// compatibility with setting rel.ValidationAssetID directly
	if len(rel.ValidationChain) == 0 {
		rel.ValidationChain = append(rel.ValidationChain, struct {
			ValidationAssetID                       int64
			ValidationAssetName, ValidationAssetURL string
		}{
			ValidationAssetID:   rel.ValidationAssetID,
			ValidationAssetName: "",
			ValidationAssetURL:  rel.ValidationAssetURL,
		})
	}

	validationName := rel.AssetName

	for _, va := range rel.ValidationChain {
		validationData, err := up.download(ctx, rel, va.ValidationAssetID)
		if err != nil {
			return fmt.Errorf("failed reading validation data %q: %w", va.ValidationAssetName, err)
		}

		if err = up.validator.Validate(validationName, data, validationData); err != nil {
			return fmt.Errorf("failed validating asset content %q: %w", validationName, err)
		}

		// Select what next to validate
		validationName = va.ValidationAssetName
		data = validationData
	}
	return nil
}

func (up *Updater) download(ctx context.Context, rel *Release, assetID int64) ([]byte, error) {
	reader, err := up.source.DownloadReleaseAsset(ctx, rel, assetID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	return io.ReadAll(reader)
}
