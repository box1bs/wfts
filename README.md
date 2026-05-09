# Проект является асинхронным поисковым роботом html страниц на англ. языке, с интегрированным поисковиком и исправлением опечаток.

## Демо
[![Video vs-code-demo](https://private-user-images.githubusercontent.com/190737632/584275984-7e03eaa9-8e8f-405a-85f7-fdacf785eca8.png?jwt=eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJnaXRodWIuY29tIiwiYXVkIjoicmF3LmdpdGh1YnVzZXJjb250ZW50LmNvbSIsImtleSI6ImtleTUiLCJleHAiOjE3NzczMDE0NzcsIm5iZiI6MTc3NzMwMTE3NywicGF0aCI6Ii8xOTA3Mzc2MzIvNTg0Mjc1OTg0LTdlMDNlYWE5LThlOGYtNDA1YS04NWY3LWZkYWNmNzg1ZWNhOC5wbmc_WC1BbXotQWxnb3JpdGhtPUFXUzQtSE1BQy1TSEEyNTYmWC1BbXotQ3JlZGVudGlhbD1BS0lBVkNPRFlMU0E1M1BRSzRaQSUyRjIwMjYwNDI3JTJGdXMtZWFzdC0xJTJGczMlMkZhd3M0X3JlcXVlc3QmWC1BbXotRGF0ZT0yMDI2MDQyN1QxNDQ2MTdaJlgtQW16LUV4cGlyZXM9MzAwJlgtQW16LVNpZ25hdHVyZT1iOWQ2MmI5OWU0NGIyYWVlMzQ1MDcyYjZhZjAxYTRhOWIwMzQ4NThkNzJjYmU4NjMzYjY3NWUxMGNkY2UzYTE5JlgtQW16LVNpZ25lZEhlYWRlcnM9aG9zdCZyZXNwb25zZS1jb250ZW50LXR5cGU9aW1hZ2UlMkZwbmcifQ.-N-DXTWgA2kgmLDN4O9fQEeIXkncWyR2qR8I5kn5na0)](https://github.com/user-attachments/assets/7866524a-54cf-4d7d-b6a0-b6b869bee8e9)
[![Video cli-demo](https://private-user-images.githubusercontent.com/190737632/584278203-3cd94758-701c-4e51-a17d-60849f677dbf.png?jwt=eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJnaXRodWIuY29tIiwiYXVkIjoicmF3LmdpdGh1YnVzZXJjb250ZW50LmNvbSIsImtleSI6ImtleTUiLCJleHAiOjE3NzczMDE2OTMsIm5iZiI6MTc3NzMwMTM5MywicGF0aCI6Ii8xOTA3Mzc2MzIvNTg0Mjc4MjAzLTNjZDk0NzU4LTcwMWMtNGU1MS1hMTdkLTYwODQ5ZjY3N2RiZi5wbmc_WC1BbXotQWxnb3JpdGhtPUFXUzQtSE1BQy1TSEEyNTYmWC1BbXotQ3JlZGVudGlhbD1BS0lBVkNPRFlMU0E1M1BRSzRaQSUyRjIwMjYwNDI3JTJGdXMtZWFzdC0xJTJGczMlMkZhd3M0X3JlcXVlc3QmWC1BbXotRGF0ZT0yMDI2MDQyN1QxNDQ5NTNaJlgtQW16LUV4cGlyZXM9MzAwJlgtQW16LVNpZ25hdHVyZT0zYjZlZTQ2OTNlYTQ0NTE5YjJlNWFkNzM5NDdiMjk0OGE1MWM3NTkwMjMwNzE1OGNiZjBhZTQxNTU5NGIzMWVlJlgtQW16LVNpZ25lZEhlYWRlcnM9aG9zdCZyZXNwb25zZS1jb250ZW50LXR5cGU9aW1hZ2UlMkZwbmcifQ.Tmh7zmblMPTCOzbwjpx1owyjrKmEmlvP-BvL5TNsyU8)](https://github.com/user-attachments/assets/57b49a4b-8cb2-4be2-896a-8b9b5669e5ad)

## Описание
Проект был создан для стабильной, многопоточной индексации небольшого объема веб страниц, на английском языке(из-за относительной простоты лингвистики этого языка), с реализацией всего, возможного функционала с помощью build-in инструментов языка golang.

### Поисковой робот
Я реализовал обработку кодировки и извлечение html контента токенизатором, извлечение sitemap.xml и robots.txt правил, переобработка уже посещенных страниц с помощью lru кэша/badger DB, для сохранения нужной для прохождения далее информации, была придумана наивная(потому что мы считаем что качество ссылок, на которые ссылается обрабатываемая страница, зависят от ее качества) формула для подсчета приоритета задач для планировщика в поисковом роботе, а так же реализована min max куча для буффера задач в планировщике и стек в файле, для сохранения задач между сессиями и против устаревания/гниения контекстного окна.

### Индексер
Я хотел бы рассказать о нем как о библиотеке скорее, чем как об отдельном компоненте, поскольку в текущей реализации его части импортируются(неявно) обоими главными пакетами. Главная задача индексера в том, чтобы предобработать текст перед сохранением(инвертированный индекс), педобработать поисковую строку перем фетчингом документов в поиске и замены слов, которые мы считаем ошибочными, с учетом контекста(словестные биграммы), контроль схожих(шинглы и min hash) документов по ряду из n схожих шинглов, а так стандартизация(стемминг алгоритм Портера) и токенизация слов, что является функциями дочерних пакетов индексера.

### Индекс
Я реализовал обратный индекс, с учетом, как числа слов в документе, так и их позиций, так же я реализовал шинглирование(minhash) для проверки схожести сканиремого документа с другими в шардах со схожими частями хэша, индексацию trigramm для испраления опечаток с помощью буффера файлов для распределения нагрузки между ними, еще было реализовано что-то вроде кэша уже обработанных страниц с изъятыми из них ссылками, для повторной обработки при необходимости, и биграммы слов для полноценной реализации модели зашумленного канала.

### Поиск
В поиске, на мой взгляд, важную роль играют как алгоритмические метрики для отбора кандидатов, по типу term frequency * inverted document frequency, bm25, и любые метрики, которые можно рассчитать на основе индекса, то есть, для примера, term proximity под которой подразумевается минимальный путь между словами запроса в документе, так и семантический(или векторный) поиск, который основывается на смысловых эмбеддингах полученных с помощью различных языковых трансформеров, по типу семейства Bert моделей, а так же модели/ей ранжирования поверх этого, с учетом уже новых метрик, для ранжирования, или реранжирования результатов, но к сожелению эту часть пришлось отбросить, из-за невозможности обучить качественную модель, без метрик ориентированных на текущий индекс(tf idf | bm25) и невозможности использования можной Bert модели, потому что я хочу оставить легковестность и нетребовательность поиска к памяти и процессору, не говоря уже о видеокарте, так же я не хочу утяжелять индексацию и замедлять поиск.

## Запуск
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