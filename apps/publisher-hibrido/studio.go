package main

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// A instalacao deste pacote no ChatGPT depende da permissao e da confirmacao
// do usuario. O Desktop nao instala plugins remotamente nem possui acesso
// a conta do ChatGPT no navegador.
//
//go:embed assets/studio_plugin.zip
var studioPluginZip []byte

const studioPluginFilename = "LoopingBR-Studio-v1.8.0-Instalacao.zip"
const studioChatGPTURL = "https://chatgpt.com/"

var studioLinkPath = regexp.MustCompile(`^/plugins/(?:plugins_|plugin_)[A-Za-z0-9_-]+/?$`)

func validateStudioURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Scheme != "https" || parsed.Host != "chatgpt.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !studioLinkPath.MatchString(parsed.Path) {
		return "", errors.New("Cole o link do seu plugin criado no ChatGPT, no formato https://chatgpt.com/plugins/plugins_...")
	}
	return parsed.String(), nil
}

// Prepara um ZIP incluido no EXE. Se ja houver arquivo modificado com o mesmo
// nome, NUNCA sobrescrever; cria nome alternativo usando hash do pacote.
func writeStudioPackage(dir string) (string, error) {
	if len(studioPluginZip) < 200 || !bytes.HasPrefix(studioPluginZip, []byte("PK\x03\x04")) {
		return "", errors.New("Pacote do Studio ausente ou invalido no executavel")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, studioPluginFilename)
	if old, err := os.ReadFile(path); err == nil {
		if bytes.Equal(old, studioPluginZip) {
			return path, nil
		}
		sum := sha256.Sum256(studioPluginZip)
		path = filepath.Join(dir, "LoopingBR-Studio-v1.8.0-"+hex.EncodeToString(sum[:4])+".zip")
		if old, err := os.ReadFile(path); err == nil {
			if bytes.Equal(old, studioPluginZip) {
				return path, nil
			}
			return "", errors.New("Arquivo do Studio ja existe e possui conteudo diferente; mova o arquivo antigo antes de continuar")
		} else if !os.IsNotExist(err) {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".loopingbr-studio-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(studioPluginZip); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", err
	}
	return path, nil
}

func studioPackageDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	// A pasta Downloads pode estar redirecionada em alguns computadores;
	// neste caso mostramos o caminho de fallback dentro do perfil v0.7.
	download := filepath.Join(home, "Downloads")
	if info, err := os.Stat(download); err == nil && info.IsDir() {
		return download, nil
	}
	return filepath.Join(filepath.Dir(cfgPath()), "Studio"), nil
}

func (a *App) studioPrepare(w http.ResponseWriter, r *http.Request) {
	dir, err := studioPackageDir()
	if err != nil {
		apiErr(w, 500, "Nao foi possivel localizar a pasta do usuario: "+err.Error())
		return
	}
	path, err := writeStudioPackage(dir)
	if err != nil {
		apiErr(w, 500, "Nao foi possivel preparar o Studio: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "Pacote preparado localmente. Arraste o arquivo para o Plugin Creator no ChatGPT e confirme a criacao.", "path": path, "filename": filepath.Base(path), "chatgpt_url": studioChatGPTURL})
}

func (a *App) studioReveal(w http.ResponseWriter, r *http.Request) {
	dir, err := studioPackageDir()
	if err != nil {
		apiErr(w, 500, "Pasta do Studio indisponivel")
		return
	}
	path, err := writeStudioPackage(dir)
	if err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	if runtime.GOOS != "windows" {
		writeJSON(w, 200, map[string]string{"ok": "Arquivo disponivel em " + path, "path": path})
		return
	}
	// Abre somente o Explorador local, sem qualquer rede ou upload automatico.
	if err := exec.Command("explorer.exe", "/select,", path).Start(); err != nil {
		apiErr(w, 500, fmt.Sprintf("Pacote salvo em %s, mas nao foi possivel abrir o Explorador: %v", path, err))
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "O arquivo do Studio foi selecionado no Explorador do Windows.", "path": path})
}

func (a *App) studioSaveLink(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL string `json:"url"`
	}
	if err := readJSON(r, &input); err != nil {
		apiErr(w, 400, "Informe um link valido do Studio")
		return
	}
	link, err := validateStudioURL(input.URL)
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	a.Lock()
	a.cfg.StudioPluginURL = link
	a.Unlock()
	if err := a.saveConfig(); err != nil {
		apiErr(w, 500, "Nao foi possivel guardar o link: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "Link da sua copia do Studio salvo neste computador.", "url": link})
}

func (a *App) studioForgetLink(w http.ResponseWriter, r *http.Request) {
	a.Lock()
	a.cfg.StudioPluginURL = ""
	a.Unlock()
	if err := a.saveConfig(); err != nil {
		apiErr(w, 500, "Nao foi possivel remover o link: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "Link removido somente deste computador. Seu plugin no ChatGPT continua intacto."})
}
