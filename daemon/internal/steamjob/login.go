package steamjob

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"syscall"

	"github.com/creack/pty"

	"elb/daemon/internal/config"
	"elb/daemon/internal/settings"
)

var (
	loginMu  sync.Mutex
	loginPty *os.File // write-конец pty текущего логина, nil — если код сейчас никто не ждёт
)

var (
	// guardPromptRe — намеренно широкий (несколько известных формулировок
	// Steam Guard в разных версиях steamcmd/типах 2FA — email-код,
	// мобильный authenticator с кодом, мобильный authenticator с простым
	// подтверждением без кода) — точный текст ПОДТВЕРЖДАЕТСЯ живым логином
	// на Phase 2 (см. project memory), сама строка-триггер уходит в
	// Job.Message как есть, чтобы UI показал администратору дословно, что
	// спрашивает steamcmd, даже если наш детектор неидеален.
	guardPromptRe = regexp.MustCompile(`(?i)steam guard|two-factor|authenticator`)
	// loginFailRe — БЕЗ (?i): живой баг 2026-09-12 — подтверждение входа
	// через мобильное приложение (без ввода кода, просто "approve" в
	// Steam Mobile) в норме заканчивается успехом, но steamcmd попутно
	// печатает locale-предупреждение "setlocale('en_US.UTF-8') failed,
	// using locale: 'C'" — регистронезависимый (?i) ловил это лишнее
	// "failed" и помечал совершенно нормальный вход как провалившийся.
	// Собственные статусы steamcmd — всегда заглавными (FAILED (...),
	// Invalid Password, Login Failure), поэтому точное совпадение
	// регистра отсекает подобный шум, не теряя реальных ошибок.
	loginFailRe = regexp.MustCompile(`FAILED|Login Failure|Invalid Password`)
)

func Login(cfg config.Config, username, password string) bool {
	if _, ok := TryBegin("login"); !ok {
		return false
	}
	go runLogin(cfg, username, password)
	return true
}

func Logout(cfg config.Config) error {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		return err
	}
	s.Steam.Username = ""
	return settings.Save(cfg.SettingsFile, s)
}

func SubmitCode(code string) bool {
	loginMu.Lock()
	f := loginPty
	loginMu.Unlock()
	if f == nil {
		return false
	}
	setState("running", "")
	_, err := f.Write([]byte(code + "\n"))
	return err == nil
}

func runLogin(cfg config.Config, username, password string) {
	cmd := exec.Command(cfg.SteamcmdBin, "+login", username, password, "+quit")

	cred, env, err := dataOwnerCredential(cfg)
	if err != nil {
		setState("failed", err.Error())
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
	cmd.Env = env
	f, err := pty.Start(cmd)
	if err != nil {
		setState("failed", fmt.Sprintf("запуск steamcmd: %v", err))
		return
	}
	loginMu.Lock()
	loginPty = f
	loginMu.Unlock()
	defer func() {
		loginMu.Lock()
		loginPty = nil
		loginMu.Unlock()
		f.Close()
	}()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	sawGuardPrompt := false
	sawFailure := false
	for scanner.Scan() {

		line := ansiRe.ReplaceAllString(scanner.Text(), "")
		appendOutput(line)
		if !sawGuardPrompt && guardPromptRe.MatchString(line) {
			sawGuardPrompt = true
			setState("awaiting_code", line)
		}
		if loginFailRe.MatchString(line) {
			sawFailure = true
		}
	}

	waitErr := cmd.Wait()
	if waitErr != nil || sawFailure {
		msg := lastOutputLine()
		if msg == "" {
			if waitErr != nil {
				msg = waitErr.Error()
			} else {
				msg = "вход не выполнен"
			}
		}
		setState("failed", msg)
		return
	}

	s, err := settings.Load(cfg.SettingsFile)
	if err == nil {
		s.Steam.Username = username
		if err := settings.Save(cfg.SettingsFile, s); err != nil {
			setState("failed", fmt.Sprintf("вход выполнен, но не удалось сохранить логин: %v", err))
			return
		}
	}
	setState("success", "")
}
