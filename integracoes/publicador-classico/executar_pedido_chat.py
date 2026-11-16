"""Executa somente um novo pedido de publicacao explicitamente aprovado.
Reserva um marcador antes do upload para evitar duplicacao em reruns.
"""
import base64
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent
REPO = os.environ.get("GITHUB_REPOSITORY", "")
API = "https://api.github.com"
TOKEN = os.environ.get("GH_JOB_TOKEN", "")
SHA = os.environ.get("GITHUB_SHA", "")


def gh_request(method, endpoint, data=None):
    if not TOKEN or not REPO:
        raise RuntimeError("GitHub token/repository missing")
    body = None if data is None else json.dumps(data).encode("utf-8")
    req = urllib.request.Request(
        API + endpoint, data=body, method=method,
        headers={
            "Authorization": "Bearer " + TOKEN,
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
            "Content-Type": "application/json",
        })
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        if error.code in (404, 422):
            raise RuntimeError(
                "GitHub status " + str(error.code) +
                " (pedido pode ja estar reservado; NAO repetir o upload)."
            ) from error
        raise


def validate(job):
    required = ["video_drive_id", "thumbnail_drive_id", "title", "description",
                "visibility", "made_for_kids", "confirm", "channel_name"]
    if not isinstance(job, dict) or any(k not in job for k in required):
        raise ValueError("Pedido nao contem todos os campos obrigatorios.")
    if job["confirm"] != "CONFIRMO_PUBLICAR":
        raise ValueError("Publicacao sem confirmacao explicita bloqueada.")
    if job["visibility"] not in ("unlisted", "private", "public"):
        raise ValueError("Visibilidade invalida.")
    if not isinstance(job["made_for_kids"], bool):
        raise ValueError("made_for_kids deve ser boolean.")
    if not isinstance(job["title"], str) or not 0 < len(job["title"]) <= 100:
        raise ValueError("Titulo invalido.")
    if not isinstance(job["description"], str) or len(job["description"]) > 5000:
        raise ValueError("Descricao invalida.")
    pat = r"[A-Za-z0-9_-]{10,100}"
    if not isinstance(job["video_drive_id"], str) or not re.fullmatch(pat, job["video_drive_id"]):
        raise ValueError("ID do video invalido.")
    thumb = job["thumbnail_drive_id"]
    if not isinstance(thumb, str) or (thumb and not re.fullmatch(pat, thumb)):
        raise ValueError("ID da thumbnail invalido.")
    expected_name = os.environ.get("YOUTUBE_CHANNEL_NAME", "").strip()
    if not expected_name or not isinstance(job["channel_name"], str) or job["channel_name"] != expected_name:
        raise RuntimeError("Canal do pedido diferente do nome configurado neste repositorio.")
    if not os.environ.get("YOUTUBE_CHANNEL_ID"):
        raise RuntimeError("ID do canal nao configurado nos Secrets.")


def main():
    paths = subprocess.check_output([
        "git", "diff-tree", "--no-commit-id", "--name-only",
        "--diff-filter=A", "-r", SHA, "--", "publicacoes/aprovadas/",
    ], text=True).splitlines()
    paths = [p for p in paths if re.fullmatch(
        r"publicacoes/aprovadas/[a-zA-Z0-9_-]{12,80}\.json", p)]
    if len(paths) != 1:
        raise RuntimeError("Exige um unico NOVO pedido JSON no commit; nenhum video enviado.")
    filename = paths[0]
    job = json.loads((ROOT / filename).read_text(encoding="utf-8"))
    validate(job)
    name = Path(filename).stem
    marker = "publicacoes/resultados/" + name + ".json"
    marker_content = json.dumps({
        "status": "iniciado_nao_repetir",
        "source": filename,
        "event_commit": SHA,
        "note": "Verifique YouTube Studio e logs antes de qualquer nova tentativa.",
    }, ensure_ascii=False, indent=2) + "\n"
    gh_request("PUT", "/repos/" + REPO + "/contents/" + marker, {
        "message": "Reservar publicacao " + name + " (evitar duplicatas)",
        "content": base64.b64encode(marker_content.encode("utf-8")).decode("ascii"),
    })
    print("Pedido reservado. Rerun bloqueado. Marcador:", marker)
    env = dict(os.environ)
    env.update({
        "VIDEO_DRIVE_ID": job["video_drive_id"],
        "THUMBNAIL_DRIVE_ID": job["thumbnail_drive_id"],
        "VIDEO_TITLE": job["title"],
        "VIDEO_DESCRIPTION": job["description"],
        "VISIBILITY": job["visibility"],
        "MADE_FOR_KIDS": str(job["made_for_kids"]).lower(),
        "CONFIRM_PUBLISH": job["confirm"],
    })
    subprocess.run([sys.executable, str(ROOT / "publicar_youtube.py")],
                   check=True, env=env)
    print("Processamento encerrado. Confira video e thumbnail no YouTube Studio.")


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print("PUBLICACAO BLOQUEADA OU INTERROMPIDA:", type(exc).__name__,
              str(exc), file=sys.stderr)
        sys.exit(1)
