"""Verifica acesso ao canal, MP4 e PNG sem publicar qualquer video."""
import os
import re
import sys
from io import BytesIO
from google.auth.transport.requests import Request
from google.oauth2.credentials import Credentials
from googleapiclient.discovery import build
from googleapiclient.http import MediaIoBaseDownload


def require(key):
    val = os.environ.get(key, "").strip()
    if not val:
        raise ValueError(f"Secret ou variavel ausente: {key}")
    return val


def main():
    video_id = require("VIDEO_DRIVE_ID")
    if not re.fullmatch(r"[A-Za-z0-9_-]{10,100}", video_id):
        raise ValueError("ID do arquivo no Drive invalido.")
    scopes = [
        "https://www.googleapis.com/auth/youtube.upload",
        "https://www.googleapis.com/auth/youtube.readonly",
        "https://www.googleapis.com/auth/drive.readonly",
    ]
    creds = Credentials(
        token=None,
        refresh_token=require("GOOGLE_REFRESH_TOKEN"),
        token_uri="https://oauth2.googleapis.com/token",
        client_id=require("GOOGLE_CLIENT_ID"),
        client_secret=require("GOOGLE_CLIENT_SECRET"),
        scopes=scopes,
    )
    creds.refresh(Request())
    print("OK: autorizacao Google renovada (sem exibir tokens).")
    youtube = build("youtube", "v3", credentials=creds, cache_discovery=False)
    channels = youtube.channels().list(part="snippet", mine=True).execute().get("items", [])
    if len(channels) != 1:
        raise RuntimeError("Canal nao identificado de forma unica.")
    if channels[0]["id"] != require("YOUTUBE_CHANNEL_ID"):
        raise RuntimeError("Canal autorizado nao corresponde ao YOUTUBE_CHANNEL_ID.")
    print("OK: canal corresponde ao ID configurado.")
    drive = build("drive", "v3", credentials=creds, cache_discovery=False)
    meta = drive.files().get(fileId=video_id, fields="id,name,mimeType,size,trashed").execute()
    if meta.get("trashed") or meta.get("size") == "0":
        raise RuntimeError("Arquivo na lixeira ou vazio.")
    if meta.get("mimeType") not in {"video/mp4", "video/quicktime", "application/octet-stream"}:
        print("AVISO: tipo do arquivo:", meta.get("mimeType"))
    print("OK: arquivo do Drive acessivel:", meta.get("name"))
    thumb_id = os.environ.get("THUMBNAIL_DRIVE_ID", "").strip()
    if thumb_id:
        if not re.fullmatch(r"[A-Za-z0-9_-]{10,100}", thumb_id):
            raise ValueError("ID da thumbnail invalido.")
        stream = BytesIO()
        loader = MediaIoBaseDownload(stream, drive.files().get_media(fileId=thumb_id))
        done = False
        while not done:
            _, done = loader.next_chunk(num_retries=3)
            if stream.tell() > 50 * 1024 * 1024:
                raise ValueError("Imagem excede o limite da verificacao (50 MB).")
        if not stream.getvalue().startswith(bytes.fromhex("89504e47")):
            raise ValueError("Thumbnail nao e PNG valido.")
        print("OK: thumbnail PNG acessivel a conta autorizada.")
    print("CONEXAO VALIDADA: nenhum video publicado.")


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print("ERRO DE VALIDACAO:", type(exc).__name__, str(exc), file=sys.stderr)
        sys.exit(1)
