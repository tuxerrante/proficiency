## 📚 Deep Dive: Go Performance & Profiling Resources

Curated highlights:

### 🔬 Go memory & allocations

- **Memory allocation in Go (2025)** – clear explanation of how Go allocates and how to reduce GC overhead  
  https://nghiant3223.github.io/2025/06/03/memory_allocation_in_go.html
- **Optimizing memory allocation in Go** – practical patterns for small/large objects, `sync.Pool`, pre-allocation[1]
  https://dev.to/jones_charles_ad50858dbc0/optimizing-memory-allocation-in-go-small-and-large-objects-made-simple-4ica
- **Go Optimization Guide** – focused patterns that appear in real codebases  
  https://goperf.dev[2]

### 🔥 pprof & profiling techniques

- **Profiling Go programs** – practical pprof intro with examples  
  https://pears.one/profiling-go-programs/
- **pprof Quick Start** – “get profiling in 10 minutes” with real-world tips[3]
  https://developer20.com/pprof-part1-quick-start/
- **Profiling on macOS is broken** – caveats when profiling on macOS and how to interpret data correctly  
  https://www.dolthub.com/blog/2025-11-07-profiling-on-mac-is-broken/
- **Diffing Go profiles** – advanced guide on comparing profiles (`-base`) for regressions  
  https://www.dolthub.com/blog/2025-06-20-go-pprof-diffing/

### 🧠 Tools & UIs

- **pproftui** – a TUI (terminal UI) for pprof from DoltHub  
  https://github.com/Oloruntobi1/pproftui
- **Hatchet Go agents** – using agents for distributed workloads, including performance-sensitive operations  
  https://docs.hatchet.run/blog/go-agents
- **Datadog: Go Swiss Tables** – deep-dive into Go's map implementation and performance implications  
  https://www.datadoghq.com/blog/engineering/go-swiss-tables/

### 📰 Discussions & ecosystem

- **HN threads & war stories** – profiling experiences and pitfalls  
  https://news.ycombinator.com/item?id=45344708
- **Go books & ecosystem overviews** (for broader context)[4][5][6]
  https://bitfieldconsulting.com/posts/best-go-books  
  https://github.com/dariubs/GoBooks  
  https://blog.jetbrains.com/go/2025/11/10/go-language-trends-ecosystem-2025/[7]

***

### Further readings
- [optimizing-memory-allocation-in-go-small-and-large-objects](https://dev.to/jones_charles_ad50858dbc0/optimizing-memory-allocation-in-go-small-and-large-objects-made-simple-4ica)
- [goperf](https://goperf.dev)
- [pprof-part1-quick-start/](https://developer20.com/pprof-part1-quick-start/)
- [dariubs/GoBooks](https://github.com/dariubs/GoBooks)
- https://techkoalainsights.com/7-essential-go-profiling-tools-every-developer-should-master-for-performance-optimization-8cc6533dac98
- https://dev.to/seyedahmaddv/profiling-in-go-a-practical-guide-to-finding-performance-bottlenecks-32e7
- https://www.youtube.com/watch?v=ZEWtoJsiAgs

