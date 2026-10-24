package iconextract

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

//go:embed extract_icons.py
var script []byte

func Extract(pythonBin, bundlePath, outDir string) (int, error) {
	if pythonBin == "" {
		return 0, fmt.Errorf("не настроен путь к Python (ELB_ICON_EXTRACT_PYTHON) — см. daemon/internal/iconextract")
	}

	tmp, err := os.CreateTemp("", "elb-extract-icons-*.py")
	if err != nil {
		return 0, fmt.Errorf("временный файл скрипта: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(script); err != nil {
		tmp.Close()
		return 0, fmt.Errorf("запись временного скрипта: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}

	cmd := exec.Command(pythonBin, tmp.Name(), bundlePath, outDir)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return 0, fmt.Errorf("extract_icons.py: %w: %s", err, text)
	}

	var n, failed int
	if _, scanErr := fmt.Sscanf(text, "OK %d failed=%d", &n, &failed); scanErr != nil {
		return 0, fmt.Errorf("extract_icons.py: неожиданный вывод: %s", text)
	}
	if failed > 0 {
		return n, fmt.Errorf("extract_icons.py: %d иконок не извлеклось (см. лог), успешно %d", failed, n)
	}
	return n, nil
}
