# Segurança e privacidade

## O que foi excluído desta edição de portfólio

- Histórico real de pedidos, identificadores de vídeos, thumbnails e arquivos do Google Drive.
- Relatórios pessoais e arquivos de verificação ligados a contas reais.
- Executáveis Windows e cachês de compilação da árvore do código público (são disponibilizados apenas no pacote Windows separado).
- Notas antigas de desenvolvimento que não fazem parte da execução do software.

## Como as credenciais devem ser tratadas

O backend solicita o OAuth Client ID e Secret localmente e os recebe por variáveis de ambiente durante a execução. A proteção dos tokens do perfil no Windows é implementada em código com DPAPI. O upload remoto requer autenticação apropriada, verificação do contexto de GitHub Actions e vínculo com o canal configurado.

Nunca envie para o GitHub tokens de OAuth, refresh tokens, arquivos do perfil, `admin.key`, `.env`, cookies, credenciais ou capturas com dados privados. A distribuição inclui `.gitignore` como barreira adicional, mas o responsável deve revisar os arquivos antes de cada commit.

As identificações públicas de aplicativos OAuth eventualmente presentes no código não são segredos equivalentes a Client Secret. Cada pessoa deverá revisar consentimento, escopos, cotas e restrições da própria configuração.

## Cuidados na operação

1. Use contas próprias e valide a identidade do canal antes de autorizar o primeiro upload.
2. Faça testes com visibilidade `unlisted`.
3. Não exponha o backend local a uma rede pública sem compreender o mecanismo de autenticação e os controles do túnel HTTPS.
4. Não reexecute uploads com resultado incerto até conferir o YouTube Studio.
5. Antes de tornar um projeto público, rode uma varredura de credenciais e revise o histórico Git — apagar um arquivo do commit atual não apaga automaticamente commits anteriores.

## Escopo desta verificação

A inspeção desta edição não encontrou credenciais privadas fornecidas pelo autor nos arquivos de código selecionados. Isso não equivale a auditoria independente de segurança nem a teste com contas reais.
