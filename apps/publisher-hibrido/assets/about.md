# LoopingBR Publisher — instalação independente

Este repositório foi criado pelo LoopingBR Publisher Desktop e pertence exclusivamente ao titular da conta GitHub que autorizou a criação. Não é o repositório original do desenvolvedor.

- As credenciais do Google ficam como **GitHub Actions Secrets** (não são incluídas nos arquivos do código).
- Cada novo pedido validado deve entrar individualmente em `publicacoes/aprovadas/*.json` e aciona `workflow.yml`.
- O executor reserva o pedido em `publicacoes/resultados/` antes do upload; reenviar jobs após falha pode duplicar vídeos e é bloqueado.
- A republicação intencional do mesmo ID de Drive exige `allow_republish: true` em novo pedido aprovado.
- Conferir o YouTube Studio após qualquer erro ou incerteza. Não utilizar o botão **Re-run jobs** para tentar reenviar.
- **Limites iniciais**: vídeo MP4 até 4 GiB, thumbnail JPEG/PNG até 50 MB. A verificação de Shorts depende das regras atuais da plataforma.
- Uploads efetuados por projetos da API do YouTube não verificados podem ficar sujeitos a limitação de privacidade, cotas e revisão do Google.
- Revogue as autorizações no Google ou GitHub se não quiser mais que o app tenha acesso.
