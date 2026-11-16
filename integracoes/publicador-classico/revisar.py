"""Revisao BASICA de formato de ID e visibilidade, sem Drive nem YouTube."""
import os
import re
video_id = os.environ['VIDEO_ID']
vis = os.environ['VISIBILITY']
if not re.fullmatch(r'[\w-]{10,100}', video_id):
    raise SystemExit('ID do Drive invalido')
if vis not in ('unlisted', 'private', 'public'):
    raise SystemExit('Visibilidade invalida')
print('Publisher: revisao basica, sem conexao ao Drive, SEM UPLOAD')
print('ID do arquivo:', video_id)
print('Visibilidade solicitada:', vis)
print('Para confirmar acesso real, rode o workflow verificar_conexao.')
