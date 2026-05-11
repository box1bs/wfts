# The project is an asynchronous HTML page crawler for English-language content, with an integrated search engine and spell-cheching.

## Demo
[![Video vs-code-demo](https://private-user-images.githubusercontent.com/190737632/584275984-7e03eaa9-8e8f-405a-85f7-fdacf785eca8.png?jwt=eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJnaXRodWIuY29tIiwiYXVkIjoicmF3LmdpdGh1YnVzZXJjb250ZW50LmNvbSIsImtleSI6ImtleTUiLCJleHAiOjE3NzczMDE0NzcsIm5iZiI6MTc3NzMwMTE3NywicGF0aCI6Ii8xOTA3Mzc2MzIvNTg0Mjc1OTg0LTdlMDNlYWE5LThlOGYtNDA1YS04NWY3LWZkYWNmNzg1ZWNhOC5wbmc_WC1BbXotQWxnb3JpdGhtPUFXUzQtSE1BQy1TSEEyNTYmWC1BbXotQ3JlZGVudGlhbD1BS0lBVkNPRFlMU0E1M1BRSzRaQSUyRjIwMjYwNDI3JTJGdXMtZWFzdC0xJTJGczMlMkZhd3M0X3JlcXVlc3QmWC1BbXotRGF0ZT0yMDI2MDQyN1QxNDQ2MTdaJlgtQW16LUV4cGlyZXM9MzAwJlgtQW16LVNpZ25hdHVyZT1iOWQ2MmI5OWU0NGIyYWVlMzQ1MDcyYjZhZjAxYTRhOWIwMzQ4NThkNzJjYmU4NjMzYjY3NWUxMGNkY2UzYTE5JlgtQW16LVNpZ25lZEhlYWRlcnM9aG9zdCZyZXNwb25zZS1jb250ZW50LXR5cGU9aW1hZ2UlMkZwbmcifQ.-N-DXTWgA2kgmLDN4O9fQEeIXkncWyR2qR8I5kn5na0)](https://github.com/user-attachments/assets/7866524a-54cf-4d7d-b6a0-b6b869bee8e9)
[![Video cli-demo](https://private-user-images.githubusercontent.com/190737632/584278203-3cd94758-701c-4e51-a17d-60849f677dbf.png?jwt=eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJnaXRodWIuY29tIiwiYXVkIjoicmF3LmdpdGh1YnVzZXJjb250ZW50LmNvbSIsImtleSI6ImtleTUiLCJleHAiOjE3NzczMDE2OTMsIm5iZiI6MTc3NzMwMTM5MywicGF0aCI6Ii8xOTA3Mzc2MzIvNTg0Mjc4MjAzLTNjZDk0NzU4LTcwMWMtNGU1MS1hMTdkLTYwODQ5ZjY3N2RiZi5wbmc_WC1BbXotQWxnb3JpdGhtPUFXUzQtSE1BQy1TSEEyNTYmWC1BbXotQ3JlZGVudGlhbD1BS0lBVkNPRFlMU0E1M1BRSzRaQSUyRjIwMjYwNDI3JTJGdXMtZWFzdC0xJTJGczMlMkZhd3M0X3JlcXVlc3QmWC1BbXotRGF0ZT0yMDI2MDQyN1QxNDQ5NTNaJlgtQW16LUV4cGlyZXM9MzAwJlgtQW16LVNpZ25hdHVyZT0zYjZlZTQ2OTNlYTQ0NTE5YjJlNWFkNzM5NDdiMjk0OGE1MWM3NTkwMjMwNzE1OGNiZjBhZTQxNTU5NGIzMWVlJlgtQW16LVNpZ25lZEhlYWRlcnM9aG9zdCZyZXNwb25zZS1jb250ZW50LXR5cGU9aW1hZ2UlMkZwbmcifQ.Tmh7zmblMPTCOzbwjpx1owyjrKmEmlvP-BvL5TNsyU8)](https://github.com/user-attachments/assets/57b49a4b-8cb2-4be2-896a-8b9b5669e5ad)

## Description
The project was created for stable, multithreaded indexing of a small volume of web pages in English (due to the relative simplicity of the language's linguistics), implementing all possible functionality using Go's built-in tools.

## Crawler
I implemented encoding handling and HTML content extraction via a tokenizer, sitemap.xml and robots.txt rule extraction, re-processing of already-visited pages using an LRU cache / BadgerDB. To preserve the information needed to continue crawling, I devised a naive formula (naive because we assume the quality of links on a given page depends on that page's own quality) for calculating task priority in the crawler's scheduler, and also implemented a min-max heap for the task buffer in the scheduler, plus an on-disk stack for persisting tasks between sessions and guarding against context-window staleness/rot.

## Indexer
I would describe it more as a library than a standalone component, since in the current implementation its parts are imported (implicitly) by both main packages. The indexer's primary job is to preprocess text before storing it in the inverted index, preprocess the search query before fetching documents, replace words considered erroneous with context awareness (word bigrams), control for near-duplicate documents (shingling and MinHash) across a run of n similar shingles, and perform standardization (Porter stemming algorithm) and word tokenization, the latter being functions of the indexer's child packages.

## Index
I implemented an inverted index that tracks both word counts and positions within documents. I also implemented shingling (MinHash) for checking the similarity of a crawled document against others in shards sharing similar hash segments, trigram indexing for spell correction using a file buffer to distribute load across files, a cache-like store of already-processed pages with their extracted links (for reprocessing when needed), and word bigrams for a full implementation of the noisy channel model.

## Search
In search, I believe algorithmic metrics for candidate selection play an important role, things like TF-IDF, BM25, and any metrics computable directly from the index, such as term proximity (meaning the minimum path between query terms within a document), as does semantic (vector) search based on meaning embeddings produced by various language transformers like the BERT model family, along with a ranking model on top of all that. Unfortunately, this part had to be dropped: training a quality model without metrics grounded in the current index (TF-IDF / BM25) was not feasible, and using a heavy BERT model was out of the question since I want to keep the search lightweight and undemanding on memory and CPU, let alone GPU, and I don't want to slow down indexing or search latency.

## Running
### Docker
```bash
mkdir .data
make docker-run
```
### Host
```bash
mkdir .data
make run
```

## Two-day benchmark
* documents idexed: 48963
* memory availability metrics:
![first day metrics](./internal/assets/may10m.png)
![second day metrics](./internal/assets/may11m.png)
* disk writes:
![first day metrics](./internal/assets/may10d.png)
![second day metrics](./internal/assets/may11d.png)
* network out:
![first day metrics](./internal/assets/may10n.png)
![second day metrics](./internal/assets/may11n.png)