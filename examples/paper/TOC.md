# Original Functionset from ReFaaS Paper

This is the dataset used for evaluation experiments in the ReFaaS paper. We collected a set of serverless functions (see Table 1) that are written in Python and manually translated them to Go.

## List of Functions (Table 1)
| Function | Type             | Description                                                     | Source |
|----------|-------------------|----------------------------------------------------------------|--------|
| f1       | Basic             | Basic "hello world" function.                                | B      |
| f2       | Basic             | Basic addition function.                                     | A      |
| f3       | Basic             | Basic calculator function with error handling.               | A      |
| f4       | Basic             | Basic percentage calculator.                                  | A      |
| f5       | Basic             | Compound interest calculator.                                 | A      |
| f6       | Basic             | Recursive Fibonacci function.                                  | A      |
| f7       | Data Structures   | Google Chat webhook function.                                  | B      |
| f8       | Basic             | Graph theory solver function.                                   | C      |
| f9       | API               | Performs HTTP GET requests to external API and returns transformed output. | D |
| f10      | API               | Fetches current weather data using OpenWeatherMap API.        | D      |
| f11      | Data Structures   | Extract and validate user information with nested data structures. | B |
| f12      | Data Structures   | Performs ELT on large JSON files.                              | A      |
| f13      | Syntactic Sugar   | Uses a decorator pattern for logging.                           | D      |
| f14      | Syntactic Sugar   | Implements a multi-stage data processing pipeline.              | A      |

> A = introductory programming tasks<br>
> B = online examples, e.g., from AWS or Google’s documentations<br>
> C = created based on CodeNet<br>
> D = created from API documentation