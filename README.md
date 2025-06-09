# Kubernetes Performance Analyzer

Uma ferramenta em Go para análise de performance e recursos em clusters Kubernetes, com foco em recomendações de otimização.

## Características

- Análise de recursos por deployment
- Coleta de métricas de CPU e memória
- Recomendações de otimização baseadas em uso real
- Suporte a múltiplos contextos do Kubernetes
- Período de coleta configurável
- Agrupamento de métricas por deployment
- Cálculo de médias e máximos de uso de recursos

## Requisitos

- Go 1.16 ou superior
- Acesso a um cluster Kubernetes
- Metrics Server instalado no cluster (opcional, para métricas em tempo real)

## Instalação

1. Clone o repositório:
```bash
git clone https://github.com/seu-usuario/k8s-performance-analyzer.git
cd k8s-performance-analyzer
```

2. Instale as dependências:
```bash
go mod download
```

3. Compile o projeto:
```bash
go build
```

## Desenvolvimento

1. Configure o ambiente de desenvolvimento:
```bash
# Inicialize o repositório git
git init

# Adicione o .gitignore primeiro
git add .gitignore
git commit -m "chore: add .gitignore"

# Adicione os arquivos do projeto
git add .
git commit -m "feat: initial commit"
```

2. Para executar durante o desenvolvimento:
```bash
go run main.go
```

3. Para testar as alterações:
```bash
go test ./...
```

## Uso

Execute o analisador com:

```bash
./k8s-performance-analyzer [opções]
```

### Opções

- `-help`: Mostra a mensagem de ajuda
- `-kubeconfig`: Caminho para o arquivo kubeconfig (opcional)
- `-context`: Nome do contexto do Kubernetes a ser usado (opcional)
- `-periodo`: Período de coleta de métricas (ex: 30m, 1h) (padrão: 5m)

### Exemplos

Analisar o cluster atual:
```bash
./k8s-performance-analyzer
```

Especificar um contexto e período:
```bash
./k8s-performance-analyzer -context meu-cluster -periodo 30m
```

Ver a ajuda:
```bash
./k8s-performance-analyzer -help
```

## Saída

O analisador gera um arquivo de recomendações no diretório `performance-reports` com:

- Recomendações agrupadas por deployment
- Métricas máximas e médias de CPU e memória
- Problemas identificados (pods sem limites)
- Sugestões de configuração de recursos
- Lista de pods monitorados

### Formato do Relatório

O relatório de recomendações inclui:

1. Informações do Deployment:
   - Nome e namespace
   - Total de pods
   - Pods sem limites de recursos

2. Métricas (quando disponíveis):
   - Uso máximo de CPU e memória
   - Média de uso de CPU e memória

3. Problemas Identificados:
   - Pods sem limites de recursos
   - Impacto e prioridade

4. Recomendações de Recursos:
   - Limites sugeridos baseados no uso máximo
   - Requests sugeridos baseados na média

5. Lista de Pods Monitorados

## Segurança

Esta ferramenta é 100% segura e não faz nenhuma alteração no cluster. Ela apenas:
- Lê informações do cluster
- Coleta métricas
- Gera relatórios locais

## Contribuindo

1. Faça um fork do projeto
2. Crie uma branch para sua feature (`git checkout -b feature/nova-feature`)
3. Faça commit das suas alterações (`git commit -am 'feat: adiciona nova feature'`)
4. Faça push para a branch (`git push origin feature/nova-feature`)
5. Crie um Pull Request

## Licença

Este projeto está licenciado sob a licença MIT - veja o arquivo [LICENSE](LICENSE) para detalhes. 