# Testes, evidências e limites

## Testes automatizados no ambiente de preparação

| Verificação | Resultado | Escopo |
| --- | --- | --- |
| `go test ./...` | **Aprovado** | Pacotes Go do Desktop e backend no Linux |
| `python -m py_compile assets/publisher.py` | **Aprovado** | Sintaxe do executor Python |
| Compilação cruzada Windows x64 | **Aprovado** | Dois executáveis PE32+ x64 gerados sem credenciais |
| `go vet ./...` | **Aprovado** | Análise estática dos pacotes Go |
| `GET /healthz` do backend local | **Aprovado** | Respondeu `{"status":"ok"}` sem credenciais |
| Login OAuth com contas reais | **Não executado** | Requer configuração de quem instalar |
| Publicação real no YouTube | **Não executada nesta edição** | Requer autorização e verificação na conta |
| Fluxo completo no Windows | **Não executado nesta edição** | Teste recomendado no computador do usuário |

A CI em `.github/workflows/ci.yml` é intencionalmente independente de contas e não cria publicações.

## Limitações conhecidas

- O executável é um aplicativo Windows e depende das APIs e permissões dos provedores externos.
- A publicação remota depende de GitHub Actions alcançar com segurança o backend do usuário via URL HTTPS.
- O YouTube pode impor limites de API, restrições de privacidade e políticas de upload independentes do software.
- Integrações com IA são opcionais; uma sugestão de texto ou análise não representa publicação automática.
- A versão atual é tratada como **LAB/protótipo**, com melhorias previstas para o instalador e os testes end-to-end.
- O módulo `integracoes/publicador-classico` é um snapshot antigo preservado por contexto histórico; o código operacional privado pode conter modificações posteriores.

## Próxima evidência a adicionar

Uma gravação curta mostrando a tela inicial, a preparação de um pedido, a revisão/confirmacão, a execução no GitHub Actions e o estado final no YouTube Studio — omitindo credenciais, identificadores privados e dados de terceiros.
