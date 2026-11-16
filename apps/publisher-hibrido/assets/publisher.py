"""Motor independente LoopingBR. Um pedido novo por commit, sem retry de upload.

Cada pedido é reservado no GitHub ANTES do upload; em erro incerto, verificar
YouTube Studio antes de criar um novo pedido. Não executar manualmente o
arquivo: o workflow chama somente após commit do pedido autorizado.
"""
from __future__ import annotations

import base64
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request
from datetime import datetime, timedelta, timezone
from urllib.parse import parse_qs, urlparse

from google.auth.transport.requests import Request
from google.oauth2.credentials import Credentials
from googleapiclient.discovery import build
from googleapiclient.errors import HttpError
from googleapiclient.http import MediaFileUpload, MediaIoBaseDownload

ROOT = Path(__file__).resolve().parent
ALLOWED_VISIBILITY = {'private', 'unlisted', 'public'}
REQUIRED = ('video_drive_id', 'thumbnail_drive_id', 'title', 'description',
            'visibility', 'made_for_kids', 'confirm', 'channel_id')
SCOPES = [
    'https://www.googleapis.com/auth/drive.readonly',
    'https://www.googleapis.com/auth/youtube.readonly',
    'https://www.googleapis.com/auth/youtube.upload',
]
MAX_VIDEO_BYTES = 4 * 1024**3
MAX_THUMB_BYTES = 50 * 1000**2


class Blocked(Exception):
    pass


def gh_api(method: str, path: str, payload=None):
    token = os.environ['GH_JOB_TOKEN']
    repo = os.environ['GITHUB_REPOSITORY']
    req = urllib.request.Request(
        f'https://api.github.com/repos/{repo}/{path}',
        data=None if payload is None else json.dumps(payload).encode('utf-8'),
        method=method, headers={
            'Accept': 'application/vnd.github+json',
            'Authorization': f'Bearer {token}',
            'X-GitHub-Api-Version': '2022-11-28',
            'Content-Type': 'application/json',
        },
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.load(resp)


def save_result(name: str, obj: dict, sha: str | None = None) -> str:
    path = f'publicacoes/resultados/{name}.json'
    content = json.dumps(obj, ensure_ascii=False, indent=2) + '\n'
    body = {'message': f'Registro da publicacao {name}',
            'content': base64.b64encode(content.encode('utf-8')).decode('ascii')}
    if sha:
        body['sha'] = sha
    resp = gh_api('PUT', f'contents/{path}', body)
    return resp['content']['sha']


def drive_id(s: str) -> str:
    if not isinstance(s, str) or not s.strip():
        raise Blocked('Informe o ID do arquivo no Drive.')
    s = s.strip()
    if s.startswith('http'):
        url = urlparse(s)
        if url.scheme != 'https' or url.hostname not in ('drive.google.com', 'docs.google.com'):
            raise Blocked('O link deve ser HTTPS do Google Drive.')
        p = url.path.strip('/').split('/')
        s = p[2] if p[:2] == ['file', 'd'] and len(p) >= 3 else parse_qs(url.query).get('id', [''])[0]
    if not re.fullmatch(r'[A-Za-z0-9_-]{10,100}', s):
        raise Blocked('ID do Drive inválido.')
    return s


def validate(req: dict) -> dict:
    if not isinstance(req, dict) or any(k not in req for k in REQUIRED):
        raise Blocked('Pedido não contém todos os campos obrigatórios.')
    if req['confirm'] != 'CONFIRMO_PUBLICAR':
        raise Blocked('Pedido não foi expressamente aprovado.')
    if req['visibility'] not in ALLOWED_VISIBILITY:
        raise Blocked('Visibilidade inválida.')
    if type(req['made_for_kids']) is not bool or type(req.get('allow_republish', False)) is not bool:
        raise Blocked('Classificação ou republicação inválida.')
    if not isinstance(req['title'], str) or not 1 <= len(req['title']) <= 100:
        raise Blocked('Título precisa ter entre 1 e 100 caracteres.')
    if not isinstance(req['description'], str) or len(req['description'].encode('utf-8')) > 5000:
        raise Blocked('Descrição precisa ter até 5.000 bytes.')
    tags = req.get('tags') or []
    if not isinstance(tags, list) or len(tags) > 25 or any(not isinstance(v, str) or not v.strip() or len(v) > 60 or ',' in v for v in tags):
        raise Blocked('Tags inválidas; use até 25 palavras-chave.')
    if sum(len(v.encode('utf-8')) for v in tags) > 440:
        raise Blocked('Tags excedem o limite permitido.')
    req['tags'] = tags
    req['video_drive_id'] = drive_id(req['video_drive_id'])
    if req['thumbnail_drive_id']:
        req['thumbnail_drive_id'] = drive_id(req['thumbnail_drive_id'])
    if req.get('tipo', 'video') not in ('video', 'shorts', 'auto'):
        raise Blocked('Tipo inválido.')
    if req['channel_id'] != os.environ.get('YOUTUBE_CHANNEL_ID'):
        raise Blocked('Canal no pedido não corresponde ao canal autorizado.')
    if req.get('publish_at'):
        if req['visibility'] != 'public':
            raise Blocked('Agendamento requer visibilidade public.')
        try:
            when = datetime.fromisoformat(req['publish_at'].replace('Z', '+00:00'))
        except (ValueError, TypeError) as exc:
            raise Blocked('Use agendamento ISO 8601 com fuso.') from exc
        if when.tzinfo is None or when.astimezone(timezone.utc) < datetime.now(timezone.utc) + timedelta(minutes=10):
            raise Blocked('Agendamento deve ser pelo menos 10 minutos no futuro.')
    return req


def new_request_path() -> Path:
    commit = os.environ['GITHUB_SHA']
    paths = subprocess.check_output([
        'git', 'diff-tree', '--no-commit-id', '--name-only', '--diff-filter=A',
        '-r', commit, '--', 'publicacoes/aprovadas/'
    ], text=True).splitlines()
    files = [p for p in paths if re.fullmatch(r'publicacoes/aprovadas/[A-Za-z0-9_-]{12,80}\.json', p)]
    if len(files) != 1:
        raise Blocked('O commit deve acrescentar exatamente um pedido aprovado.')
    return ROOT / files[0]


class BrokerGoogleCredentials(Credentials):
    """Obtém acessos Google de curta duração no backend autenticado por OIDC.

    O repositório NÃO recebe client_secret nem refresh_token permanentes.
    Cada renovação recebe identidade GitHub assinada e de curta validade.
    """
    def __init__(self):
        super().__init__(token=None, scopes=SCOPES)

    def refresh(self, request):
        from urllib.parse import urlencode, urlsplit, urlunsplit, parse_qsl
        backend = os.environ.get('LOOPINGBR_BROKER_URL', '').strip().rstrip('/')
        parsed = urlparse(backend)
        if parsed.scheme != 'https' or not parsed.hostname or parsed.username or parsed.password or parsed.path or parsed.query:
            raise Blocked('Backend HTTPS não configurado no repositório')
        oidc_url = os.environ.get('ACTIONS_ID_TOKEN_REQUEST_URL', '')
        oidc_key = os.environ.get('ACTIONS_ID_TOKEN_REQUEST_TOKEN', '')
        if not oidc_url.startswith('https://') or not oidc_key:
            raise Blocked('Permissão GitHub Actions id-token: write indisponível')
        url_parts = urlsplit(oidc_url)
        params = dict(parse_qsl(url_parts.query, keep_blank_values=True))
        params['audience'] = 'loopingbr-hybrid-lab-v1'
        url = urlunsplit((url_parts.scheme, url_parts.netloc, url_parts.path,
                          urlencode(params), url_parts.fragment))
        identity_req = urllib.request.Request(url, headers={
            'Authorization': 'Bearer ' + oidc_key,
            'Accept': 'application/json',
        })
        try:
            with urllib.request.urlopen(identity_req, timeout=25) as identity_response:
                identity = json.load(identity_response)['value']
        except Exception as exc:
            raise Blocked('Falha ao obter identidade GitHub Actions OIDC') from exc
        if not isinstance(identity, str) or len(identity) > 16000:
            raise Blocked('Token OIDC inválido')
        request_body = json.dumps({'channel_id': os.environ['YOUTUBE_CHANNEL_ID']}).encode()
        broker_req = urllib.request.Request(backend + '/api/runner/token',
            data=request_body, method='POST', headers={
                'Authorization': 'Bearer ' + identity,
                'Content-Type': 'application/json', 'Accept': 'application/json'})
        try:
            with urllib.request.urlopen(broker_req, timeout=30) as broker_response:
                value = json.load(broker_response)
        except urllib.error.HTTPError as exc:
            raise Blocked('Backend recusou esta execução (HTTP %d); verificar vínculo e túnel HTTPS' % exc.code) from exc
        except Exception as exc:
            raise Blocked('Backend indisponível; verificar túnel HTTPS e computador ligado') from exc
        token = value.get('access_token', '')
        if not isinstance(token, str) or len(token) < 20 or value.get('channel_id') != os.environ['YOUTUBE_CHANNEL_ID']:
            raise Blocked('Resposta do backend inválida ou canal divergente')
        expires = max(180, min(int(value.get('expires_in', 3000)), 3600))
        self.token = token
        self.expiry = datetime.now(timezone.utc).replace(tzinfo=None) + timedelta(seconds=expires - 90)


def google_services():
    if not os.environ.get('LOOPINGBR_BROKER_URL') or not os.environ.get('YOUTUBE_CHANNEL_ID'):
        raise Blocked('Backend híbrido ou canal não configurado.')
    creds = BrokerGoogleCredentials()
    creds.refresh(Request())
    youtube = build('youtube', 'v3', credentials=creds, cache_discovery=False)
    drive = build('drive', 'v3', credentials=creds, cache_discovery=False)
    channels = youtube.channels().list(part='id,snippet', mine=True).execute().get('items', [])
    if len(channels) != 1 or channels[0]['id'] != os.environ['YOUTUBE_CHANNEL_ID']:
        raise Blocked('O canal da autorização Google é diferente do canal confirmado.')
    print('Canal validado:', channels[0]['snippet']['title'])
    return drive, youtube


def download(drive, id_: str, dst: Path, allowed: set[str], limit: int):
    meta = drive.files().get(fileId=id_, fields='id,name,mimeType,size,trashed').execute()
    size = int(meta.get('size') or 0)
    mime = meta.get('mimeType', '')
    if meta.get('trashed') or not (0 < size <= limit) or mime not in allowed:
        raise Blocked('Arquivo do Drive inválido, inacessível, muito grande ou com MIME não aceito.')
    # Arquivos de 4 GiB precisam caber no runner antes de começar o download.
    # Reservamos 1 GiB para dependências e arquivos temporários; nenhuma tentativa extra.
    free = shutil.disk_usage(dst.parent).free
    if free < size + 1024**3:
        raise Blocked('Espaço insuficiente no GitHub Actions para processar este vídeo. Nenhum upload ao YouTube iniciado.')
    with dst.open('wb') as target:
        downloader = MediaIoBaseDownload(target, drive.files().get_media(fileId=id_),
                                        chunksize=8 * 1024 * 1024)
        finished = False
        while not finished:
            _, finished = downloader.next_chunk(num_retries=2)
    if dst.stat().st_size != size:
        raise Blocked('Download incompleto ou muito grande.')
    return mime


def check_video(video: Path, tipo: str):
    process = subprocess.run(['ffprobe', '-v', 'error', '-show_entries',
        'format=duration:stream=codec_type,width,height', '-of', 'json', str(video)],
        capture_output=True, text=True)
    if process.returncode != 0:
        raise Blocked('O arquivo de vídeo não pôde ser lido pelo ffprobe.')
    data = json.loads(process.stdout)
    videos = [s for s in data.get('streams', []) if s.get('codec_type') == 'video']
    if not videos:
        raise Blocked('O arquivo não possui faixa de vídeo.')
    stream = videos[0]
    dur = float(data.get('format', {}).get('duration', 0))
    w, h = int(stream.get('width', 0)), int(stream.get('height', 0))
    if dur <= 0 or not w or not h:
        raise Blocked('Duração ou dimensão inválida.')
    is_short = w <= h and dur <= 180
    if tipo == 'shorts' and not is_short:
        raise Blocked('Para Shorts: formato vertical/quadrado e duração máxima de 3 minutos.')
    if tipo == 'auto':
        tipo = 'shorts' if is_short else 'video'
    print(f'Arquivo validado: {w}x{h}, {dur:.1f}s; tipo {tipo}')
    return tipo


def upload(req: dict) -> dict:
    drive, youtube = google_services()
    with tempfile.TemporaryDirectory(prefix='loopingbr-') as tmp:
        video = Path(tmp) / 'video.mp4'
        mime = download(drive, req['video_drive_id'], video, {'video/mp4'}, MAX_VIDEO_BYTES)
        tipo = check_video(video, req.get('tipo', 'video'))
        thumb = None
        thumb_mime = None
        if req['thumbnail_drive_id'] and tipo != 'shorts':
            thumb = Path(tmp) / 'thumbnail'
            thumb_mime = download(drive, req['thumbnail_drive_id'], thumb,
                                  {'image/png', 'image/jpeg'}, MAX_THUMB_BYTES)
            data = thumb.open('rb').read(12)
            if not (data.startswith(bytes.fromhex('89504e470d0a1a0a')) or data.startswith(bytes.fromhex('ffd8ff'))):
                raise Blocked('Thumbnail não é PNG/JPEG válido.')
        scheduled = bool(req.get('publish_at'))
        status = {'privacyStatus': 'private' if scheduled else req['visibility'],
                  'selfDeclaredMadeForKids': req['made_for_kids']}
        if scheduled:
            status['publishAt'] = datetime.fromisoformat(req['publish_at'].replace('Z', '+00:00')).astimezone(timezone.utc).isoformat().replace('+00:00', 'Z')
        print('INICIANDO UPLOAD. Em falha incerta, verificar YouTube Studio ANTES de tentar novamente.')
        call = youtube.videos().insert(part='snippet,status',
            body={'snippet': {'title': req['title'], 'description': req['description'], 'tags': req.get('tags', []), 'categoryId': '20'},
                  'status': status},
            media_body=MediaFileUpload(str(video), mimetype=mime, resumable=True, chunksize=8*1024*1024))
        response = None
        while response is None:
            _, response = call.next_chunk(num_retries=2)
        youtube_id = response.get('id')
        if not youtube_id:
            raise Blocked('Resposta do upload sem ID. Verificar Studio antes de tentar novamente.')
        result = {'status': 'agendado' if scheduled else 'enviado',
                  'youtube_video_id': youtube_id, 'youtube_url': 'https://youtu.be/' + youtube_id,
                  'thumbnail_ok': thumb is None}
        print('Vídeo enviado:', result['youtube_url'])
        if thumb:
            try:
                youtube.thumbnails().set(videoId=youtube_id,
                    media_body=MediaFileUpload(str(thumb), mimetype=thumb_mime)).execute()
                result['thumbnail_ok'] = True
                print('Thumbnail aplicada.')
            except HttpError as e:
                result['thumbnail_ok'] = False
                print('AVISO: Vídeo enviado mas thumbnail falhou (HTTP',e.resp.status,'). Ajustar no Studio; não repostar.')
        return result


def main():
    path = new_request_path()
    req = validate(json.loads(path.read_text(encoding='utf-8')))
    if not req.get('allow_republish'):
        for old in (ROOT / 'publicacoes' / 'aprovadas').glob('*.json'):
            if old == path:
                continue
            try:
                previous = json.loads(old.read_text(encoding='utf-8'))
            except (ValueError, OSError) as exc:
                raise Blocked('Existe pedido anterior ilegível; envio bloqueado.') from exc
            if drive_id(previous.get('video_drive_id', '')) == req['video_drive_id']:
                raise Blocked('Vídeo do Drive já consta em outro pedido. Autorize republicação somente após conferir Studio.')
    name = path.stem
    info = {'status': 'reservado_nao_reexecutar', 'pedido': str(path.relative_to(ROOT)),
            'commit': os.environ['GITHUB_SHA'], 'allow_republish': bool(req.get('allow_republish')),
            'nota': 'Não dar rerun. Conferir YouTube Studio após falha.'}
    try:
        marker_sha = save_result(name, info)  # atomically fails if path already exists
    except urllib.error.HTTPError as exc:
        raise Blocked('Pedido já reservado ou não foi possível reservá-lo; nenhum upload será iniciado.') from exc
    try:
        result = upload(req)
    except Exception as exc:
        info['status'] = 'interrompido_verificar_studio'
        info['erro_tipo'] = type(exc).__name__
        try:
            save_result(name, info, marker_sha)
        except Exception:
            pass
        raise
    info.update(result)
    try:
        save_result(name, info, marker_sha)
    except Exception:
        print('AVISO: Upload concluído, mas falhou registrar resultado. Conferir YouTube Studio; não reenviar.')
    print('Processamento finalizado. Conferir YouTube Studio.')


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print('PUBLICAÇÃO INTERROMPIDA:', type(error).__name__, str(error), file=sys.stderr)
        sys.exit(1)
