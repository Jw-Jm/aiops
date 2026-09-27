package profile

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
	"ops-platform/internal/contract"
)

func ReadProfile(reader io.Reader) (InputProfile, error) {
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	var input InputProfile
	if err := decoder.Decode(&input); err != nil {
		return InputProfile{}, fmt.Errorf("decode Deployment Profile: %w", err)
	}
	if err := input.ValidateTemplate(); err != nil {
		return InputProfile{}, err
	}
	if err := validateProfileSchema(input); err != nil {
		return InputProfile{}, err
	}
	return input, nil
}

func validateProfileSchema(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode Deployment Profile: %w", err)
	}
	if err := contract.Validate("https://ops.local/schemas/deployment-profile/v1", payload); err != nil {
		return fmt.Errorf("Deployment Profile Schema: %w", err)
	}
	return nil
}

func ReadProfileFile(path string) (InputProfile, error) {
	file, err := os.Open(path)
	if err != nil {
		return InputProfile{}, fmt.Errorf("open Deployment Profile %q: %w", path, err)
	}
	defer file.Close()
	return ReadProfile(file)
}

func WriteYAML(path string, value any) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create profile output %q: %w", path, err)
	}
	encoder := yaml.NewEncoder(file)
	encoder.SetIndent(2)
	encodeErr := encoder.Encode(value)
	closeErr := encoder.Close()
	fileErr := file.Close()
	if encodeErr != nil {
		return fmt.Errorf("write profile output: %w", encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("finish profile output: %w", closeErr)
	}
	if fileErr != nil {
		return fmt.Errorf("close profile output: %w", fileErr)
	}
	return nil
}
