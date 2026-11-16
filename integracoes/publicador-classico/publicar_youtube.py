"""LoopingBR Publisher: envio aprovado pelo GitHub Actions.
O token de acesso ao Google existe apenas nos GitHub Actions Secrets.
"""
import os
import re
import sys
import tempfile
from pathlib import Path
from google.oauth2.credentials import Credentials
from google.auth.transport.requests import Request
from googleapiclient.discovery import build
from googleapiclient.http import MediaIoBaseDownload, MediaFileUpload


def required(name):
    value = os.environ.get(name, "").strip()
    if not value:
        raise ValueError(f"Configuracao ausente: {name}")
    return value


def main():
    if os.environ.get("CONFIRM_PUBLISH") != "CONFIRMO_PUBLICAR":
        raise ValueError("Publicacao bloqueada: confirmacao explicita ausente.")
    video_id = required("VIDEO_DRIVE_ID")
    if not re.fullmatch(r"[A-Za-z0-9_-]{10,100}", video_id):
        raise ValueError("ID do Drive invalido.")
    visibility = required("VISIBILITY")
    if visibility not in {"public", "unlisted", "private"}:
        raise ValueError("Visibilidade invalida.")
    audience = required("MADE_FOR_KIDS")
    if audience not in {"true", "false"}:
        raise ValueError("Informe made_for_kids true/false.")
    title = required("VIDEO_TITLE")
    description = os.environ.get("VIDEO_DESCRIPTION", "")
    if len(title) > 100 or len(description) > 5000:
        raise ValueError("Titulo ou descricao excedeu o limite do YouTube.")
    thumb_id = os.environ.get("THUMBNAIL_DRIVE_ID", "").strip()
    if thumb_id and not re.fullmatch(r"[A-Za-z0-9_-]{10,100}", thumb_id):
        raise ValueError("ID da thumbnail invalido.")

    scopes = ["https://www.googleapis.com/auth/youtube.upload",
              "https://www.googleapis.com/auth/youtube.readonly",
              "https://www.googleapis.com/auth/drive.readonly"]
    credentials = Credentials(
        token=None,
        refresh_token=required("GOOGLE_REFRESH_TOKEN"),
        token_uri="https://oauth2.googleapis.com/token",
        client_id=required("GOOGLE_CLIENT_ID"),
        client_secret=required("GOOGLE_CLIENT_SECRET"),
        scopes=scopes,
    )
    credentials.refresh(Request())
    youtube = build("youtube", "v3", credentials=credentials, cache_discovery=False)
    drive = build("drive", "v3", credentials=credentials, cache_discovery=False)

    channels = youtube.channels().list(part="snippet", mine=True).execute().get("items", [])
    if len(channels) != 1:
        raise RuntimeError("Nao foi possivel identificar um canal unico autorizado.")
    expected = required("YOUTUBE_CHANNEL_ID")
    if channels[0]["id"] != expected:
        raise RuntimeError("Canal autorizado diferente do canal aprovado; envio bloqueado.")
    print("Canal validado:", channels[0]["snippet"].get("title", ""))

    with tempfile.TemporaryDirectory() as temp:
        path = Path(temp) / "video.mp4"
        request = drive.files().get_media(fileId=video_id)
        with path.open("wb") as stream:
            downloader = MediaIoBaseDownload(stream, request, chunksize=8 * 1024 * 1024)
            done = False
            while not done:
                _, done = downloader.next_chunk(num_retries=3)
        if path.stat().st_size == 0:
            raise RuntimeError("Arquivo do Drive vazio.")
        print("Video obtido do Drive; preparando envio.")
        thumb = None
        if thumb_id:
            thumb = Path(temp) / "thumbnail.png"
            try:
                request = drive.files().get_media(fileId=thumb_id)
                with thumb.open("wb") as stream:
                    downloader = MediaIoBaseDownload(stream, request)
                    done = False
                    while not done:
                        _, done = downloader.next_chunk(num_retries=3)
                if not thumb.read_bytes().startswith(bytes.fromhex("89504e47")):
                    raise ValueError("A thumbnail nao e um PNG valido.")
                print("Thumbnail acessivel e validada antes do upload.")
            except Exception as exc:
                raise RuntimeError(
                    "Thumbnail inacessivel para a conta Google autorizada. "
                    "Nenhum upload foi iniciado. Verifique o acesso ao Drive."
                ) from exc

        body = {
            "snippet": {"title": title, "description": description, "categoryId": "20"},
            "status": {"privacyStatus": visibility, "selfDeclaredMadeForKids": audience == "true"},
        }
        media = MediaFileUpload(str(path), mimetype="video/mp4", resumable=True, chunksize=8 * 1024 * 1024)
        upload = youtube.videos().insert(part="snippet,status", body=body, media_body=media)
        response = None
        while response is None:
            _, response = upload.next_chunk(num_retries=3)
        published_id = response["id"]
        print("ID do video enviado:", published_id)
        print("URL:", "https://www.youtube.com/watch?v=" + published_id)
        if thumb is not None:
            try:
                youtube.thumbnails().set(
                    videoId=published_id,
                    media_body=MediaFileUpload(str(thumb), mimetype="image/png"),
                ).execute()
                print("Thumbnail personalizada enviada.")
            except Exception as exc:
                print(
                    "AVISO: VIDEO ENVIADO MAS THUMBNAIL FALHOU. "
                    "Corrija somente a miniatura no YouTube Studio. "
                    "NAO execute novamente o upload.", type(exc).__name__,
                )
        print("Confira visibilidade e processamento no YouTube Studio.")


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print("ERRO:", type(exc).__name__, str(exc), file=sys.stderr)
        sys.exit(1)
