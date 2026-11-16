#!/usr/bin/env python3
"""Importa apenas código do repositório operacional exportado em ZIP.

Não transporta pedidos de publicação, resultados, verificações ou credenciais.
Foi criado para facilitar a atualização da cópia técnica depois que o titular
baixar 'Code > Download ZIP' do GitHub privado.
"""
import argparse
from pathlib import Path
import re
import shutil
import zipfile

ALLOW = {
    'executar_pedido_chat.py', 'executar_manual.py', 'publicar_avancado.py',
    'publicar_youtube.py', 'revisar.py', 'verificar_conexao.py',
    'requirements-upload.txt',
    '.github/workflows/publicar_chatgpt.yml',
    '.github/workflows/publicar_youtube.yml',
    '.github/workflows/revisar.yml',
    '.github/workflows/verificar_conexao.yml',
    '.github/workflows/verificar_video_avancado.yml',
}
SECRET_PATTERNS = [
    re.compile(r'gh[opurs]_[A-Za-z0-9_]{30,}'),
    re.compile(r'github_pat_[A-Za-z0-9_]{30,}'),
    re.compile(r'GOCSPX-[A-Za-z0-9_-]+'),
    re.compile(r'AIza[0-9A-Za-z_-]{30,}'),
    re.compile(r'-----BEGIN [A-Z ]*PRIVATE KEY-----'),
]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('zip_original', type=Path, help='ZIP baixado do repositório original privado')
    parser.add_argument('--destino', type=Path, default=Path(__file__).resolve().parents[1] / 'integracoes' / 'publicador-classico')
    args = parser.parse_args()
    if not args.zip_original.is_file():
        parser.error('Arquivo ZIP não encontrado')
    content = {}
    with zipfile.ZipFile(args.zip_original) as z:
        for item in z.infolist():
            if item.is_dir():
                continue
            name = Path(item.filename)
            parts = name.parts
            if len(parts) <= 1 or any(p in ('..', '.') for p in parts):
                continue
            rel = '/'.join(parts[1:])
            if rel not in ALLOW:
                continue
            if item.file_size > 250_000:
                raise ValueError(f'Arquivo muito grande, revisão manual necessária: {rel}')
            data = z.read(item)
            text = data.decode('utf-8')
            if any(pattern.search(text) for pattern in SECRET_PATTERNS):
                raise ValueError(f'Potencial credencial no arquivo {rel}; nada foi importado')
            content[rel] = data
    if not content:
        raise ValueError('Nenhum arquivo compatível encontrado; confira se é o ZIP de código do GitHub')
    # Apenas escreve depois da inspeção completa. Nunca deleta arquivos existentes.
    for rel, data in content.items():
        path = args.destino / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
    print(f'Importados {len(content)} arquivos de código em {args.destino}')
    print('Pedidos, resultados, dados de conta e tokens não foram importados.')
    print('Revise os arquivos antes de tornar o repositório público e execute os testes de novo.')


if __name__ == '__main__':
    main()
