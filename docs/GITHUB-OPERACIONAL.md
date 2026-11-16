# Repositório operacional e sincronização

O projeto usa um repositório privado para processar pedidos reais. O original analisado foi `kfeernandesk/Loopingbr_publisher`, branch `main`, no commit `145b96d5e7837a1354f12ad218930b4dc15ee03e` (13/11/2026 UTC).

O arquivo de portfólio não inclui os pedidos `publicacoes/aprovadas/*.json`, resultados, registros de diagnóstico nem outros dados associados a arquivos reais. Esses dados não são necessários para compilar ou entender a arquitetura, e divulgá-los seria desnecessário.

## Para atualizar os scripts clássicos com a versão atual do GitHub

1. Entre na conta proprietária do GitHub.
2. Abra o repositório privado operacional e escolha **Code > Download ZIP**.
3. Na raiz do repositório de portfólio, execute:

   ```powershell
   python ferramentas/importar-codigo-github.py "C:\\Users\\Voce\\Downloads\\Loopingbr_publisher-main.zip"
   ```

4. O importador copia apenas uma lista explícita de scripts Python, arquivos de dependência e workflows. Pedidos reais, resultados e registros ficam de fora.
5. Revise o diff e teste novamente antes de publicar. O importador procura padrões comuns de segredo, mas não substitui uma auditoria completa.

**Observação:** o `publicador-classico` incluído inicialmente é uma exportação histórica de desenvolvimento. Não deve ser apresentado como uma cópia byte a byte do estado atual do repositório privado, até concluir essa importação.

O programa híbrido atual em `apps/publisher-hibrido` tem executor e workflow próprios e não depende deste módulo histórico para executar.
