package output

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/akshatsinha007/kubuto/pkg/types"
)

// JSONFormatter formats scan results as JSON.
type JSONFormatter struct {
	Pretty bool
}

// Format writes the scan result as JSON to the writer.
func (f *JSONFormatter) Format(w io.Writer, result *types.ScanResult) error {
	var data []byte
	var err error

	if f.Pretty {
		data, err = json.MarshalIndent(result, "", "  ")
	} else {
		data, err = json.Marshal(result)
	}

	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	_, err = w.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write JSON output: %w", err)
	}

	// Add newline for readability
	_, err = w.Write([]byte("\n"))
	return err
}
