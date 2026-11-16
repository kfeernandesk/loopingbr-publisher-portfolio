# Instalação e configuração

Este guia mostra como reproduzir o aplicativo em um ambiente **Windows 64 bits**. Ele não depende das credenciais nem do canal usado pelo autor. O projeto está em estágio de laboratório, portanto as integrações externas devem ser configuradas e testadas individualmente.

## Pré-requisitos

- Windows 10/11 x64 para executar a interface compilada; Go 1.23 ou superior para compilar o código.
- Python 3.12 para o executor no GitHub Actions (a instalação local do aplicativo não exige necessariamente Python).
- Conta própria do GitHub, conta Google/YouTube e acesso a um projeto do Google Cloud com OAuth.
- APIs que forem usadas habilitadas no projeto Google: YouTube Data API v3, Google Drive API e, para métricas, YouTube Analytics API.
- Conexão com a internet para autenticação e chamadas às APIs. Para o runner GitHub alcançar o backend local, é necessária uma URL HTTPS acessível com segurança.

## 1. Baixar e compilar

Clone este repositório ou baixe seu ZIP. Entre na pasta `apps/publisher-hibrido`.

No PowerShell:

```powershell
# Faça o teste inicial da base
& go test ./...
# Gera dois executáveis Windows com a configuração de build original
.\GERAR-EXES-LAB.bat
```

O script produz:

- `LoopingBR-Publisher-Hybrid-Desktop-LAB.exe`
- `LoopingBR-Hybrid-Backend-LAB.exe`

Uma alternativa é obter esses dois executáveis no pacote Windows demonstrativo, quando disponibilizado pelo autor. Executáveis não precisam ser armazenados no histórico Git.

## 2. Configurar OAuth no Google Cloud

Crie um projeto Google Cloud para uso próprio. Habilite as APIs necessárias e configure a tela de consentimento OAuth, adicionando sua conta como usuária de teste quando aplicável.

Crie um **OAuth Client do tipo Aplicativo da Web**, com URI de redirecionamento exata:

```text
http://127.0.0.1:8765/oauth/google/callback
```

Guarde o **Client ID** e o **Client Secret** para informar localmente ao backend. Não inclua o segredo em commits, issues, prints ou exemplos públicos. A configuração OAuth do YouTube pode exigir escopos sujeitos a revisão do Google; projetos de API não verificados podem ter restrições de publicação.

## 3. Iniciar o backend local

Execute, na mesma pasta:

```powershell
.\INICIAR-BACKEND-LAB.bat
```

O script solicita o Client ID e o Client Secret no computador. O backend utiliza `127.0.0.1:8765` e armazena dados de sessão no perfil local. O script não grava esses valores em arquivos de código.

Para que um workflow remoto solicite um token efêmero ao backend, é necessário configurar uma URL HTTPS válida e acessível a partir do runner. O script aceita informar essa URL na inicialização. O funcionamento dessa etapa depende da rede, do túnel HTTPS e das permissões escolhidas pelo usuário; **não exponha o backend indiscriminadamente**. O modo local sem túnel permite testar partes da interface e autenticação, mas não a publicação remota completa.

## 4. Abrir o aplicativo

Em uma segunda janela:

```powershell
.\ABRIR-DESKTOP-LAB.bat
```

A interface abre em um endereço local `127.0.0.1` com porta dinâmica. Ela guia a vinculação das contas e mostra o estado de cada integração.

## 5. Conectar GitHub, YouTube e serviços opcionais

1. No painel, conecte sua conta GitHub, aprovando as permissões solicitadas.
2. Associe sua conta Google e confirme o canal YouTube pretendido.
3. Conecte Google Drive se quiser utilizar vídeos e thumbnails por lá; o fluxo também contempla arquivos locais.
4. Configure a integração com ChatGPT apenas se desejar assistência para textos e análise.
5. Siga o fluxo guiado para preparar o repositório operacional privado usado pelo processamento de publicações.

O código do app disponibiliza um workflow e executor Python em `apps/publisher-hibrido/assets/`. O repositório operacional criado pela aplicação é diferente deste repositório público de portfólio.

## 6. Primeiro teste

Faça o primeiro envio com um MP4 pequeno, de sua autoria, visibilidade **Não listado**, título e descrição de teste. Revise os dados e confirme somente quando estiver seguro da conta e do arquivo selecionado.

**Não confunda pedido criado com vídeo publicado.** Após aprovação, acompanhe o GitHub Actions e o YouTube Studio. Se o resultado ficar incerto, verifique o Studio antes de qualquer reexecução para evitar duplicações.

## 7. Comandos úteis

```powershell
# Testar unidades Go
& go test ./...
# Validar o executor Python se tiver Python instalado
& python -m py_compile assets/publisher.py
# Recompilar Windows
.\GERAR-EXES-LAB.bat
```

## Solução de problemas

- **Não abre a interface:** confira se os dois executáveis foram gerados e inicie primeiro o backend.
- **OAuth não conclui:** confira o tipo do client, o endereço de callback e a conta autorizada.
- **Publicação não inicia:** confirme se GitHub Actions consegue alcançar o backend via URL HTTPS e se a conta corresponde ao canal configurado.
- **Upload falhou depois de começar:** não clique em reexecutar automaticamente. Confira o YouTube Studio e os registros da execução.
- **Métricas indisponíveis:** dados do Analytics dependem da permissão, do canal e da disponibilidade da API. Ausência de dados não significa zero visualizações.

Este guia documenta o que o código oferece; os testes reais com credenciais precisam ocorrer no ambiente de cada pessoa.
